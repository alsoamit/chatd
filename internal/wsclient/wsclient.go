// Package wsclient maintains a persistent connection from the daemon to
// the relay. Responsibilities:
//
//   - dial + auth handshake
//   - reconnect with exponential backoff
//   - serialize all outbound writes
//   - pump inbound frames into a channel
//   - heartbeat loop
//   - bounded outbound queue while disconnected (offline buffer)
//
// The Client is single-instance per daemon and is fed by the
// conversation manager via Send(). Frames received from the relay are
// posted on Events().
package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cedrx/chatd/internal/protocol"
	"github.com/gorilla/websocket"
)

// Config configures the client.
type Config struct {
	URL            string
	Username       string
	Token          string
	Heartbeat      time.Duration
	BackoffMin     time.Duration
	BackoffMax     time.Duration
	OfflineQueue   int // capacity of buffered outbound frames while down
	Logger         *log.Logger
	WriteTimeout   time.Duration
	ConnectTimeout time.Duration
	// ReadWindow is the per-frame read deadline. The relay pings every
	// ~54s, so this must comfortably exceed that. When zero we default
	// to max(4*Heartbeat, 90s).
	ReadWindow time.Duration
}

func (c *Config) defaults() {
	if c.Heartbeat == 0 {
		c.Heartbeat = 20 * time.Second
	}
	if c.BackoffMin == 0 {
		c.BackoffMin = 500 * time.Millisecond
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.OfflineQueue == 0 {
		c.OfflineQueue = 256
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 8 * time.Second
	}
	if c.ReadWindow == 0 {
		w := 4 * c.Heartbeat
		if w < 90*time.Second {
			w = 90 * time.Second
		}
		c.ReadWindow = w
	}
}

// Client is a relay-bound websocket. Use New + Run.
type Client struct {
	cfg Config

	events chan any  // delivered to the daemon: protocol.MessageRecv etc
	out    chan any  // app -> conn (queued while down)
	state  chan bool // online/offline transitions

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool
}

// New constructs a client with bounded outbound queue.
func New(cfg Config) *Client {
	cfg.defaults()
	return &Client{
		cfg:    cfg,
		events: make(chan any, 256),
		out:    make(chan any, cfg.OfflineQueue),
		state:  make(chan bool, 4),
	}
}

// Events returns inbound frames as decoded protocol.* values.
func (c *Client) Events() <-chan any { return c.events }

// State emits true when the client connects, false when it disconnects.
func (c *Client) State() <-chan bool { return c.state }

// Send queues an outbound frame. If the queue is full while we're
// disconnected, the oldest frame is dropped — the alternative is to
// block forever. The boolean reports whether anything was dropped.
func (c *Client) Send(v any) bool {
	select {
	case c.out <- v:
		return false
	default:
		// drop oldest, retry once
		select {
		case <-c.out:
		default:
		}
		select {
		case c.out <- v:
		default:
		}
		return true
	}
}

// IsConnected reports the current state without blocking.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Run drives the connection until ctx is cancelled. It blocks; run it in
// its own goroutine.
func (c *Client) Run(ctx context.Context) {
	backoff := c.cfg.BackoffMin
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		c.cfg.Logger.Printf("wsclient: disconnected: %v; retrying in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > c.cfg.BackoffMax {
			backoff = c.cfg.BackoffMax
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: c.cfg.ConnectTimeout}
	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectTimeout)
	defer cancel()

	conn, _, err := dialer.DialContext(dialCtx, c.cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	// Auth handshake.
	if err := writeJSON(conn, protocol.Auth{
		Type: protocol.TypeAuth, Username: c.cfg.Username, Token: c.cfg.Token,
	}, c.cfg.WriteTimeout); err != nil {
		_ = conn.Close()
		return fmt.Errorf("auth send: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("auth read: %w", err)
	}
	env, err := protocol.UnmarshalEnvelope(raw)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("auth decode: %w", err)
	}
	if env.Type == protocol.TypeAuthError {
		var ae protocol.AuthError
		_ = json.Unmarshal(raw, &ae)
		_ = conn.Close()
		return fmt.Errorf("auth error: %s", ae.Message)
	}
	if env.Type != protocol.TypeAuthOK {
		_ = conn.Close()
		return fmt.Errorf("auth: unexpected first frame %q", env.Type)
	}
	var ok protocol.AuthOK
	_ = json.Unmarshal(raw, &ok)

	// Connected. Wire up.
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()
	select {
	case c.state <- true:
	default:
	}
	c.deliver(ok)
	c.cfg.Logger.Printf("wsclient: connected as %s, %d users online", ok.Self, len(ok.Users))

	// Any inbound frame — pong or app — resets the read deadline.
	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadWindow))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(c.cfg.ReadWindow))
	})

	// readLoop posts inbound frames.
	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(conn) }()

	// heartbeat ticker
	hb := time.NewTicker(c.cfg.Heartbeat)
	defer hb.Stop()

	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.connected = false
		c.mu.Unlock()
		select {
		case c.state <- false:
		default:
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return err
		case v := <-c.out:
			if err := writeJSON(conn, v, c.cfg.WriteTimeout); err != nil {
				// requeue and bail
				_ = c.tryRequeue(v)
				return fmt.Errorf("write: %w", err)
			}
		case <-hb.C:
			if err := writeJSON(conn, protocol.Heartbeat{Type: protocol.TypeHeartbeat}, c.cfg.WriteTimeout); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
		}
	}
}

func (c *Client) tryRequeue(v any) bool {
	select {
	case c.out <- v:
		return true
	default:
		return false
	}
}

func (c *Client) readLoop(conn *websocket.Conn) error {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		// Any inbound frame is liveness evidence — bump the deadline.
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadWindow))
		env, err := protocol.UnmarshalEnvelope(raw)
		if err != nil {
			c.cfg.Logger.Printf("wsclient: bad frame: %v", err)
			continue
		}
		decoded, err := decode(env.Type, raw)
		if err != nil {
			c.cfg.Logger.Printf("wsclient: decode %s: %v", env.Type, err)
			continue
		}
		c.deliver(decoded)
	}
}

func (c *Client) deliver(v any) {
	select {
	case c.events <- v:
	default:
		// Drop oldest to keep up with a slow consumer.
		<-c.events
		c.events <- v
	}
}

func decode(typ string, raw []byte) (any, error) {
	switch typ {
	case protocol.TypeAuthOK:
		var v protocol.AuthOK
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeAuthError:
		var v protocol.AuthError
		return v, json.Unmarshal(raw, &v)
	case protocol.TypePresence:
		var v protocol.Presence
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeUserList:
		var v protocol.UserList
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeMessageRecv:
		var v protocol.MessageRecv
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeMessageAck:
		var v protocol.MessageAck
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeHistoryResponse:
		var v protocol.HistoryResponse
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeTypingRecv:
		var v protocol.TypingRecv
		return v, json.Unmarshal(raw, &v)
	case protocol.TypeError:
		var v protocol.Error
		return v, json.Unmarshal(raw, &v)
	default:
		return nil, errors.New("unknown frame type: " + typ)
	}
}

func writeJSON(conn *websocket.Conn, v any, timeout time.Duration) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	return conn.WriteJSON(v)
}
