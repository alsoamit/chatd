package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// Handler is the daemon-side callback set per accepted connection.
// Implementations are typically methods on the conversation manager.
type Handler interface {
	OnHello(*Conn, Hello) error
	OnLine(*Conn, string, []byte) error
	OnClose(*Conn)
}

// Server is the daemon's IPC listener.
type Server struct {
	path    string
	ln      net.Listener
	handler Handler
	logger  *log.Logger

	mu    sync.Mutex
	conns map[*Conn]struct{}
}

// NewServer constructs but does not start the listener. Call Start.
func NewServer(socketPath string, h Handler, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		path:    socketPath,
		handler: h,
		logger:  logger,
		conns:   map[*Conn]struct{}{},
	}
}

// SetHandler attaches a handler post-construction so callers can break
// the manager <-> server cyclic dependency.
func (s *Server) SetHandler(h Handler) { s.handler = h }

// Start binds the unix socket. Stale sockets from a previous run are
// removed; we also chmod 0600 so other users on the host cannot snoop.
func (s *Server) Start() error {
	if err := os.RemoveAll(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.ln = ln
	return nil
}

// Stop closes the listener and all live connections.
func (s *Server) Stop() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.conns = map[*Conn]struct{}{}
	s.mu.Unlock()
	_ = os.Remove(s.path)
}

// Serve accepts connections until ctx cancels or Stop is called.
func (s *Server) Serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.Stop()
	}()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// transient errors; brief pause to avoid tight loop
			time.Sleep(50 * time.Millisecond)
			continue
		}
		conn := newConn(c)
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

// Broadcast sends v to every connection that matches the predicate.
// Connections are skipped silently on write failure.
func (s *Server) Broadcast(v any, where func(*Conn) bool) {
	s.mu.Lock()
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		if where == nil || where(c) {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Write(v)
	}
}

func (s *Server) handleConn(c *Conn) {
	defer func() {
		s.handler.OnClose(c)
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = c.Close()
	}()
	r := bufio.NewReaderSize(c.raw, 8*1024)
	first := true
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		op, err := PeekOp(line)
		if err != nil {
			_ = c.Write(Error{Op: OpError, Message: "bad frame"})
			continue
		}
		if first {
			if op != OpHello {
				_ = c.Write(Error{Op: OpError, Message: "hello required"})
				return
			}
			var hello Hello
			if err := json.Unmarshal(line, &hello); err != nil {
				_ = c.Write(Error{Op: OpError, Message: "bad hello"})
				return
			}
			if err := s.handler.OnHello(c, hello); err != nil {
				_ = c.Write(Error{Op: OpError, Message: err.Error()})
				return
			}
			first = false
			continue
		}
		if err := s.handler.OnLine(c, op, line); err != nil {
			s.logger.Printf("ipc: handler %s: %v", op, err)
		}
	}
}
