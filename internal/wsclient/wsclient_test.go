package wsclient

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cedrx/chatd/internal/protocol"
	"github.com/gorilla/websocket"
)

// fakeRelay accepts a WS connection, replies to auth with auth_ok, and
// echoes anything else back as a MessageRecv (just a stand-in to test
// the read path).
type fakeRelay struct {
	mu       sync.Mutex
	conns    int
	failNext bool // simulate a transient auth failure
}

func (f *fakeRelay) handler() http.Handler {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns++
		fail := f.failNext
		f.failNext = false
		f.mu.Unlock()

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var a protocol.Auth
		_ = json.Unmarshal(raw, &a)

		if fail {
			_ = conn.WriteJSON(protocol.AuthError{Type: protocol.TypeAuthError, Message: "no"})
			_ = conn.Close()
			return
		}
		_ = conn.WriteJSON(protocol.AuthOK{Type: protocol.TypeAuthOK, Self: a.Username})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestClientConnectsAndReceivesAuthOK(t *testing.T) {
	f := &fakeRelay{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(Config{
		URL: wsURL(srv), Username: "alice", Token: "x",
		Heartbeat: 100 * time.Millisecond, BackoffMin: 50 * time.Millisecond,
		Logger: log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case ev := <-c.Events():
		if ok, isOK := ev.(protocol.AuthOK); !isOK || ok.Self != "alice" {
			t.Errorf("expected auth_ok for alice, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auth_ok")
	}
	if !c.IsConnected() {
		t.Error("expected connected")
	}
}

func TestClientReconnectsOnDrop(t *testing.T) {
	f := &fakeRelay{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	c := New(Config{
		URL: wsURL(srv), Username: "alice", Token: "x",
		Heartbeat: 50 * time.Millisecond, BackoffMin: 50 * time.Millisecond,
		BackoffMax: 100 * time.Millisecond,
		ReadWindow: 200 * time.Millisecond,
		Logger:     log.New(io.Discard, "", 0),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Wait for first connect.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !c.IsConnected() {
		time.Sleep(20 * time.Millisecond)
	}
	if !c.IsConnected() {
		t.Fatal("first connect timed out")
	}

	// Force a server-side close by killing test server and bringing
	// up a new one on the same URL.  Easier alternative: we rely on
	// the heartbeat write deadline — close the server.
	srv.CloseClientConnections()

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := f.conns
		f.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.mu.Lock()
	n := f.conns
	f.mu.Unlock()
	if n < 2 {
		t.Errorf("expected reconnect; only %d connects", n)
	}
}

func TestClientSendDropsOldestWhenQueueFull(t *testing.T) {
	c := New(Config{
		URL: "ws://nowhere", Username: "x", Token: "x",
		OfflineQueue: 2,
		Logger:       log.New(io.Discard, "", 0),
	})
	if c.Send("a") {
		t.Error("first send should not drop")
	}
	if c.Send("b") {
		t.Error("second send should not drop")
	}
	if !c.Send("c") {
		t.Error("third send should report dropped")
	}
}
