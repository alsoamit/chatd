package ipc

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recordingHandler struct {
	mu      sync.Mutex
	hello   []Hello
	lines   []string
	closed  int
	helloCh chan struct{}
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{helloCh: make(chan struct{}, 4)}
}
func (r *recordingHandler) OnHello(c *Conn, h Hello) error {
	r.mu.Lock()
	r.hello = append(r.hello, h)
	r.mu.Unlock()
	c.SetRole(h.Role)
	c.SetPeer(h.Peer)
	_ = c.Write(HelloOK{Op: OpHelloOK, Self: "alice"})
	r.helloCh <- struct{}{}
	return nil
}
func (r *recordingHandler) OnLine(_ *Conn, op string, _ []byte) error {
	r.mu.Lock()
	r.lines = append(r.lines, op)
	r.mu.Unlock()
	return nil
}
func (r *recordingHandler) OnClose(_ *Conn) {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
}

func newServer(t *testing.T, h Handler) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "ipc.sock")
	s := NewServer(sock, h, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, sock
}

func TestIPCRoundTrip(t *testing.T) {
	rec := newRecordingHandler()
	s, sock := newServer(t, rec)
	go s.Serve(context.Background())

	c := NewClient(sock)
	if err := c.Dial(5, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ok, err := c.Hello(RoleDashboard, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok.Self != "alice" {
		t.Errorf("hello_ok wrong: %+v", ok)
	}

	if err := c.Write(UsersReq{Op: OpUsers}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		n := len(rec.lines)
		rec.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec.mu.Lock()
	if len(rec.lines) != 1 || rec.lines[0] != OpUsers {
		t.Errorf("got %v", rec.lines)
	}
	rec.mu.Unlock()
}

func TestIPCRequiresHelloFirst(t *testing.T) {
	rec := newRecordingHandler()
	s, sock := newServer(t, rec)
	go s.Serve(context.Background())

	c := NewClient(sock)
	if err := c.Dial(5, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// send a non-hello frame first
	_ = c.Write(UsersReq{Op: OpUsers})
	line, err := c.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	op, _ := PeekOp(line)
	if op != OpError {
		t.Errorf("expected error, got %s", op)
	}
}

func TestBroadcastFiltersByPredicate(t *testing.T) {
	rec := newRecordingHandler()
	s, sock := newServer(t, rec)
	go s.Serve(context.Background())

	mkClient := func(role, peer string) *Client {
		c := NewClient(sock)
		if err := c.Dial(5, 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Hello(role, peer); err != nil {
			t.Fatal(err)
		}
		return c
	}
	dash := mkClient(RoleDashboard, "")
	defer dash.Close()
	cv := mkClient(RoleConversation, "alice")
	defer cv.Close()

	// wait for both hellos to register
	for i := 0; i < 2; i++ {
		select {
		case <-rec.helloCh:
		case <-time.After(time.Second):
			t.Fatal("hello timeout")
		}
	}

	s.Broadcast(Unread{Op: OpUnread, Peer: "alice", Count: 7},
		func(c *Conn) bool { return c.Role() == RoleDashboard })

	// hello_ok was already consumed inside Hello; the next line is the
	// broadcast we just sent.
	line, err := dash.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	op, _ := PeekOp(line)
	if op != OpUnread {
		t.Errorf("dashboard expected unread, got %s", op)
	}
}
