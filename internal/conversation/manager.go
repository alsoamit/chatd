// Package conversation manages the daemon's view of conversations: who
// is online, per-peer histories, unread counters, and which IPC clients
// (dashboard / chat-client) are subscribed. It is the only thing
// permitted to mutate that state — Relay events and IPC events are
// funnelled through Run.
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/cedrx/chatd/internal/ipc"
	"github.com/cedrx/chatd/internal/notifications"
	"github.com/cedrx/chatd/internal/protocol"
	"github.com/cedrx/chatd/internal/storage"
	"github.com/cedrx/chatd/internal/terminal"
	"github.com/cedrx/chatd/internal/wsclient"
	"github.com/google/uuid"
)

// Spawner abstracts terminal-window creation so we can stub it in tests.
type Spawner interface {
	Spawn(title string, argv []string) error
}

// Config wires the manager up.
type Config struct {
	Username       string
	RelayURL       string
	Storage        *storage.Store
	WS             *wsclient.Client
	IPC            *ipc.Server
	Spawner        Spawner
	Notifier       *notifications.Notifier
	ChatClientPath string   // absolute path to chat-client binary
	ChatClientArgs []string // additional args (e.g. ["--socket", "/path"])
	Logger         *log.Logger
}

// Manager owns runtime conversation state.
type Manager struct {
	cfg Config

	mu       sync.RWMutex
	users    map[string]bool   // username -> online
	unread   map[string]uint64 // peer -> count cached from storage
	subPeer  map[string]int    // peer -> # of conversation subscribers
	pending  map[string]string // pending message id -> peer (for ack routing)
	relayUp  bool
	dashOpen bool

	logger *log.Logger
}

// New constructs a Manager. The storage handle and the wsclient are
// owned by the caller; Manager only reads/writes them.
func New(cfg Config) *Manager {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	m := &Manager{
		cfg:     cfg,
		users:   map[string]bool{},
		unread:  map[string]uint64{},
		subPeer: map[string]int{},
		pending: map[string]string{},
		logger:  cfg.Logger,
	}
	if cfg.Storage != nil {
		if all, err := cfg.Storage.AllUnread(); err == nil {
			for k, v := range all {
				m.unread[k] = v
			}
		}
	}
	return m
}

// --- IPC handler interface --------------------------------------------

// OnHello completes the per-connection registration after the IPC
// server has parsed the Hello frame.
func (m *Manager) OnHello(c *ipc.Conn, h ipc.Hello) error {
	switch h.Role {
	case ipc.RoleDashboard:
		c.SetRole(ipc.RoleDashboard)
		m.mu.Lock()
		m.dashOpen = true
		m.mu.Unlock()
	case ipc.RoleConversation:
		if err := protocol.ValidateUsername(h.Peer); err != nil {
			return err
		}
		c.SetRole(ipc.RoleConversation)
		c.SetPeer(h.Peer)
		// Mark conversation read since a window is now live.
		m.mu.Lock()
		m.subPeer[h.Peer]++
		m.unread[h.Peer] = 0
		m.mu.Unlock()
		_ = m.cfg.Storage.ClearUnread(h.Peer)
		m.broadcastUnread(h.Peer, 0)
	case ipc.RoleControl:
		c.SetRole(ipc.RoleControl)
	default:
		return errors.New("unknown role")
	}
	if err := c.Write(ipc.HelloOK{
		Op: ipc.OpHelloOK, Self: m.cfg.Username, Username: m.cfg.Username,
		RelayUp: m.RelayUp(),
	}); err != nil {
		return err
	}
	if c.Role() == ipc.RoleDashboard {
		m.sendUserList(c)
	}
	if c.Role() == ipc.RoleConversation {
		m.sendHistory(c, h.Peer)
	}
	return nil
}

// OnLine handles every post-Hello frame.
func (m *Manager) OnLine(c *ipc.Conn, op string, raw []byte) error {
	switch op {
	case ipc.OpSend:
		var s ipc.Send
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		return m.handleSend(c, s)
	case ipc.OpHistory:
		var r ipc.HistoryReq
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		m.sendHistory(c, r.Peer)
		return nil
	case ipc.OpUsers:
		m.sendUserList(c)
		return nil
	case ipc.OpOpen:
		var o ipc.OpenReq
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		return m.openConversationWindow(o.Peer)
	case ipc.OpReadAll:
		var r ipc.ReadAll
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		m.markRead(r.Peer)
		return nil
	case ipc.OpStatus:
		c.Write(ipc.StatusData{
			Op: ipc.OpStatusData, Username: m.cfg.Username,
			RelayURL: m.cfg.RelayURL,
			RelayUp:  m.RelayUp(), Peers: m.UserCount(),
		})
		return nil
	case ipc.OpTyping:
		var t ipc.Typing
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		m.cfg.WS.Send(protocol.Typing{Type: protocol.TypeTyping, To: t.Peer, Active: t.Active})
		return nil
	case ipc.OpPing:
		c.Write(ipc.Pong{Op: ipc.OpPong})
		return nil
	case ipc.OpQuit:
		return errors.New("client quit")
	default:
		return c.Write(ipc.Error{Op: ipc.OpError, Message: "unknown op: " + op})
	}
}

// OnClose tracks subscription teardown when an IPC connection drops.
func (m *Manager) OnClose(c *ipc.Conn) {
	switch c.Role() {
	case ipc.RoleConversation:
		m.mu.Lock()
		if n := m.subPeer[c.Peer()] - 1; n <= 0 {
			delete(m.subPeer, c.Peer())
		} else {
			m.subPeer[c.Peer()] = n
		}
		m.mu.Unlock()
	case ipc.RoleDashboard:
		m.mu.Lock()
		m.dashOpen = false
		m.mu.Unlock()
	}
}

// --- relay-side dispatch ----------------------------------------------

// HandleRelayEvent processes one frame received from the relay.
func (m *Manager) HandleRelayEvent(ev any) {
	switch v := ev.(type) {
	case protocol.AuthOK:
		m.mu.Lock()
		m.relayUp = true
		for _, u := range v.Users {
			if u.Name != m.cfg.Username {
				m.users[u.Name] = u.Online
			}
		}
		m.mu.Unlock()
		m.broadcastRelayState(true)
		m.broadcastUserList()

	case protocol.Presence:
		if v.Username == m.cfg.Username {
			return
		}
		m.mu.Lock()
		m.users[v.Username] = v.Online
		m.mu.Unlock()
		m.broadcastPresence(v.Username, v.Online)

	case protocol.MessageRecv:
		m.handleMessageRecv(v)

	case protocol.MessageAck:
		m.handleAck(v)

	case protocol.HistoryResponse:
		m.handleHistoryResponse(v)

	case protocol.TypingRecv:
		m.cfg.IPC.Broadcast(ipc.TypingFrom{Op: ipc.OpTypingFrom, From: v.From, Active: v.Active},
			func(c *ipc.Conn) bool { return c.Role() == ipc.RoleConversation && c.Peer() == v.From })
	}
}

// HandleRelayState reacts to up/down transitions of the websocket.
func (m *Manager) HandleRelayState(up bool) {
	m.mu.Lock()
	m.relayUp = up
	if !up {
		// Mark everyone offline locally; relay will redeliver presence
		// on reconnect.
		for k := range m.users {
			m.users[k] = false
		}
	}
	m.mu.Unlock()
	m.broadcastRelayState(up)
	m.broadcastUserList()
}

// --- internals ---------------------------------------------------------

func (m *Manager) handleSend(c *ipc.Conn, s ipc.Send) error {
	body, err := protocol.ValidateBody(s.Body)
	if err != nil {
		return c.Write(ipc.Error{Op: ipc.OpError, Message: err.Error()})
	}
	if err := protocol.ValidateUsername(s.Peer); err != nil {
		return c.Write(ipc.Error{Op: ipc.OpError, Message: err.Error()})
	}
	id := uuid.NewString()
	m.mu.Lock()
	m.pending[id] = s.Peer
	m.mu.Unlock()
	dropped := m.cfg.WS.Send(protocol.MessageSend{
		Type: protocol.TypeMessageSend, ID: id, To: s.Peer, Body: body,
	})
	if dropped {
		m.logger.Printf("conversation: outbound queue dropped frame for %s", s.Peer)
	}
	// Echo to local UI so user sees their message immediately even
	// before the relay's MessageRecv echo arrives.
	echo := protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: id,
		From: m.cfg.Username, To: s.Peer, Body: body, TS: protocol.NowMillis(),
	}
	if err := m.cfg.Storage.AppendMessage(s.Peer, echo); err != nil {
		m.logger.Printf("conversation: storage append: %v", err)
	}
	m.broadcastMessage(s.Peer, echo, true)
	return nil
}

func (m *Manager) handleMessageRecv(rec protocol.MessageRecv) {
	if rec.From == m.cfg.Username {
		// Echo of our own send — already stored locally; ignore to
		// avoid duplicate.
		m.mu.Lock()
		delete(m.pending, rec.ID)
		m.mu.Unlock()
		return
	}
	peer := rec.From
	if err := m.cfg.Storage.AppendMessage(peer, rec); err != nil {
		m.logger.Printf("conversation: append: %v", err)
	}

	subscribed := false
	m.mu.RLock()
	subscribed = m.subPeer[peer] > 0
	m.mu.RUnlock()

	if !subscribed {
		// No live conversation window — bump unread, spawn one, notify.
		n, _ := m.cfg.Storage.IncrementUnread(peer)
		m.mu.Lock()
		m.unread[peer] = n
		m.mu.Unlock()
		m.broadcastUnread(peer, n)

		if err := m.openConversationWindow(peer); err != nil {
			m.logger.Printf("conversation: spawn window for %s: %v", peer, err)
		}
		if m.cfg.Notifier != nil {
			m.cfg.Notifier.Notify("chatd: "+peer, rec.Body)
		}
	}
	m.broadcastMessage(peer, rec, false)
}

func (m *Manager) handleAck(a protocol.MessageAck) {
	m.mu.Lock()
	peer, ok := m.pending[a.ID]
	delete(m.pending, a.ID)
	m.mu.Unlock()
	if !ok {
		peer = ""
	}
	m.cfg.IPC.Broadcast(ipc.Ack{Op: ipc.OpAck, ID: a.ID, Delivered: a.Delivered},
		func(c *ipc.Conn) bool {
			if c.Role() != ipc.RoleConversation {
				return false
			}
			return peer == "" || c.Peer() == peer
		})
}

func (m *Manager) handleHistoryResponse(h protocol.HistoryResponse) {
	if err := m.cfg.Storage.MergeHistory(h.Peer, h.Messages); err != nil {
		m.logger.Printf("conversation: history merge: %v", err)
	}
	m.cfg.IPC.Broadcast(toIPCHistory(h.Peer, m.cfg.Username, h.Messages),
		func(c *ipc.Conn) bool {
			return c.Role() == ipc.RoleConversation && c.Peer() == h.Peer
		})
}

func (m *Manager) markRead(peer string) {
	if peer == "" {
		return
	}
	_ = m.cfg.Storage.ClearUnread(peer)
	m.mu.Lock()
	m.unread[peer] = 0
	m.mu.Unlock()
	m.broadcastUnread(peer, 0)
}

// openConversationWindow spawns a Ghostty (or fallback) window running
// chat-client subscribed to peer.
func (m *Manager) openConversationWindow(peer string) error {
	if err := protocol.ValidateUsername(peer); err != nil {
		return err
	}
	argv := append([]string{m.cfg.ChatClientPath}, m.cfg.ChatClientArgs...)
	argv = append(argv, "--peer", peer)
	title := terminal.SafeTitle("CHAT — " + peer)
	return m.cfg.Spawner.Spawn(title, argv)
}

func (m *Manager) sendUserList(c *ipc.Conn) {
	rows := m.snapshotUsers()
	_ = c.Write(ipc.UsersData{Op: ipc.OpUsersData, Users: rows})
}

func (m *Manager) snapshotUsers() []ipc.UserRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]ipc.UserRow, 0, len(m.users))
	for u, on := range m.users {
		rows = append(rows, ipc.UserRow{Name: u, Online: on, Unread: m.unread[u]})
	}
	// Stable alphabetical sort for the dashboard.
	sortUsers(rows)
	return rows
}

func (m *Manager) sendHistory(c *ipc.Conn, peer string) {
	if err := protocol.ValidateUsername(peer); err != nil {
		return
	}
	// Local first
	msgs, err := m.cfg.Storage.History(peer, 200)
	if err == nil && len(msgs) > 0 {
		_ = c.Write(toIPCHistory(peer, m.cfg.Username, msgs))
	}
	// Then ask the relay for backlog (will trigger a HistoryResponse
	// event once it arrives).
	if m.RelayUp() {
		m.cfg.WS.Send(protocol.HistoryRequest{Type: protocol.TypeHistoryRequest, Peer: peer, Limit: 200})
	}
}

func (m *Manager) broadcastUserList() {
	rows := m.snapshotUsers()
	m.cfg.IPC.Broadcast(ipc.UsersData{Op: ipc.OpUsersData, Users: rows},
		func(c *ipc.Conn) bool { return c.Role() == ipc.RoleDashboard })
}

func (m *Manager) broadcastPresence(user string, online bool) {
	m.cfg.IPC.Broadcast(ipc.Presence{Op: ipc.OpPresence, User: user, Online: online}, nil)
	m.broadcastUserList()
}

func (m *Manager) broadcastUnread(peer string, n uint64) {
	m.cfg.IPC.Broadcast(ipc.Unread{Op: ipc.OpUnread, Peer: peer, Count: n},
		func(c *ipc.Conn) bool { return c.Role() == ipc.RoleDashboard })
}

func (m *Manager) broadcastRelayState(up bool) {
	m.cfg.IPC.Broadcast(ipc.StatusData{
		Op: ipc.OpStatusData, Username: m.cfg.Username,
		RelayURL: m.cfg.RelayURL, RelayUp: up,
		Peers: m.UserCount(),
	}, nil)
}

func (m *Manager) broadcastMessage(peer string, rec protocol.MessageRecv, self bool) {
	msg := ipc.Message{
		Op: ipc.OpMessage, Peer: peer, From: rec.From, Body: rec.Body,
		TS: rec.TS, ID: rec.ID, Self: self || rec.From == m.cfg.Username,
	}
	// Send to subscribers of this conversation
	m.cfg.IPC.Broadcast(msg, func(c *ipc.Conn) bool {
		return c.Role() == ipc.RoleConversation && c.Peer() == peer
	})
}

// RelayUp returns the most recent relay state.
func (m *Manager) RelayUp() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.relayUp
}

// UserCount is the current online-peer count.
func (m *Manager) UserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, on := range m.users {
		if on {
			n++
		}
	}
	return n
}

// Run blocks until ctx is done. It funnels relay events into the manager.
func (m *Manager) Run(ctx context.Context) {
	// initial: nudge ws to start
	relayState := m.cfg.WS.State()
	relayEvents := m.cfg.WS.Events()

	hb := time.NewTicker(30 * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-relayEvents:
			m.HandleRelayEvent(ev)
		case up := <-relayState:
			m.HandleRelayState(up)
		case <-hb.C:
			// periodic dashboard refresh keeps unread/presence in sync
			m.broadcastUserList()
		}
	}
}

func toIPCHistory(peer, self string, msgs []protocol.MessageRecv) ipc.HistoryData {
	out := make([]ipc.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ipc.Message{
			Op: ipc.OpMessage, Peer: peer, From: m.From, Body: m.Body,
			TS: m.TS, ID: m.ID, Self: m.From == self,
		})
	}
	return ipc.HistoryData{Op: ipc.OpHistoryData, Peer: peer, Messages: out}
}
