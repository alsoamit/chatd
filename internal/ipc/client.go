package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client is the per-window side of the IPC channel. The dashboard and
// chat-client both wrap one of these.
type Client struct {
	socketPath string

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
}

func NewClient(socketPath string) *Client { return &Client{socketPath: socketPath} }

// Dial connects to the daemon socket. Tries up to attempts times,
// waiting per attempt — useful immediately after Ghostty spawns the
// chat-client process while the daemon may still be coming up.
func (c *Client) Dial(attempts int, every time.Duration) error {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.reader = bufio.NewReaderSize(conn, 8*1024)
			c.mu.Unlock()
			return nil
		}
		lastErr = err
		time.Sleep(every)
	}
	return fmt.Errorf("ipc dial: %w", lastErr)
}

// Hello sends the initial role frame and waits for HelloOK.
func (c *Client) Hello(role, peer string) (HelloOK, error) {
	if err := c.Write(Hello{Op: OpHello, Role: role, Peer: peer}); err != nil {
		return HelloOK{}, err
	}
	line, err := c.ReadLine()
	if err != nil {
		return HelloOK{}, err
	}
	op, err := PeekOp(line)
	if err != nil {
		return HelloOK{}, err
	}
	if op == OpError {
		var e Error
		_ = json.Unmarshal(line, &e)
		return HelloOK{}, errors.New(e.Message)
	}
	if op != OpHelloOK {
		return HelloOK{}, fmt.Errorf("unexpected op %q", op)
	}
	var ok HelloOK
	if err := json.Unmarshal(line, &ok); err != nil {
		return HelloOK{}, err
	}
	return ok, nil
}

// Write encodes v as NDJSON.
func (c *Client) Write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("ipc: not connected")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.conn.Write(data)
	return err
}

// ReadLine reads a single NDJSON frame.
func (c *Client) ReadLine() ([]byte, error) {
	c.mu.Lock()
	r := c.reader
	c.mu.Unlock()
	if r == nil {
		return nil, errors.New("ipc: not connected")
	}
	return r.ReadBytes('\n')
}

// Close shuts down the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
