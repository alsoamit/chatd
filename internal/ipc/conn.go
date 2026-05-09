package ipc

import (
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
)

// Conn wraps an accepted net.Conn with framing helpers and per-connection
// metadata accessible to the IPC Handler.
type Conn struct {
	raw  net.Conn
	id   uint64
	role atomic.Value // string
	peer atomic.Value // string

	wmu sync.Mutex
}

var connSeq atomic.Uint64

func newConn(c net.Conn) *Conn {
	conn := &Conn{raw: c, id: connSeq.Add(1)}
	conn.role.Store("")
	conn.peer.Store("")
	return conn
}

// ID is a process-local unique identifier; useful for logging.
func (c *Conn) ID() uint64 { return c.id }

// Role returns the role declared in Hello.
func (c *Conn) Role() string { v, _ := c.role.Load().(string); return v }

// Peer returns the conversation peer (only meaningful when Role==conversation).
func (c *Conn) Peer() string { v, _ := c.peer.Load().(string); return v }

// SetRole sets the role; called by the handler after parsing Hello.
func (c *Conn) SetRole(r string) { c.role.Store(r) }

// SetPeer sets the peer.
func (c *Conn) SetPeer(p string) { c.peer.Store(p) }

// Write encodes v as a single NDJSON frame and writes it.
func (c *Conn) Write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.raw.Write(data)
	return err
}

// Close releases the underlying socket.
func (c *Conn) Close() error { return c.raw.Close() }
