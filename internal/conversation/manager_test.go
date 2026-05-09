package conversation

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/cedrx/chatd/internal/ipc"
	"github.com/cedrx/chatd/internal/protocol"
	"github.com/cedrx/chatd/internal/storage"
	"github.com/cedrx/chatd/internal/wsclient"
)

type stubSpawner struct {
	mu    sync.Mutex
	calls []string
}

func (s *stubSpawner) Spawn(title string, _ []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, title)
	return nil
}
func (s *stubSpawner) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

func newManager(t *testing.T) (*Manager, *stubSpawner, *storage.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sp := &stubSpawner{}
	ws := wsclient.New(wsclient.Config{
		URL:      "ws://nowhere",
		Username: "alice",
		Token:    "x",
	})
	srv := ipc.NewServer(filepath.Join(dir, "ipc.sock"), nil, nil)
	m := New(Config{
		Username:       "alice",
		Storage:        store,
		WS:             ws,
		IPC:            srv,
		Spawner:        sp,
		ChatClientPath: "/bin/true",
	})
	return m, sp, store
}

func TestIncomingMessageSpawnsWindowAndIncrementsUnread(t *testing.T) {
	m, sp, store := newManager(t)
	m.HandleRelayEvent(protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: "1",
		From: "bob", To: "alice", Body: "yo", TS: 1,
	})
	if got := sp.Calls(); len(got) != 1 || got[0] != "CHAT — bob" {
		t.Errorf("expected one spawn for bob, got %v", got)
	}
	if n, _ := store.Unread("bob"); n != 1 {
		t.Errorf("expected unread=1 got %d", n)
	}
	hist, _ := store.History("bob", 10)
	if len(hist) != 1 {
		t.Errorf("expected 1 stored msg, got %d", len(hist))
	}
}

func TestSubscribedConversationDoesNotBumpUnread(t *testing.T) {
	m, sp, store := newManager(t)
	// Pretend a conversation client is already subscribed to bob.
	m.mu.Lock()
	m.subPeer["bob"] = 1
	m.mu.Unlock()

	m.HandleRelayEvent(protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: "x",
		From: "bob", To: "alice", Body: "yo", TS: 1,
	})
	if n, _ := store.Unread("bob"); n != 0 {
		t.Errorf("subscribed conv should not increment unread, got %d", n)
	}
	if calls := sp.Calls(); len(calls) != 0 {
		t.Errorf("should not spawn when already subscribed: %v", calls)
	}
}

func TestPresenceUpdatesUserList(t *testing.T) {
	m, _, _ := newManager(t)
	m.HandleRelayEvent(protocol.AuthOK{
		Type: protocol.TypeAuthOK, Self: "alice",
		Users: []protocol.User{{Name: "alice", Online: true}, {Name: "bob", Online: true}},
	})
	if !m.RelayUp() {
		t.Error("relay should be up")
	}
	if m.UserCount() != 1 {
		t.Errorf("expected 1 peer (bob), got %d", m.UserCount())
	}
	m.HandleRelayEvent(protocol.Presence{
		Type: protocol.TypePresence, Username: "bob", Online: false,
	})
	if m.UserCount() != 0 {
		t.Errorf("after offline expected 0, got %d", m.UserCount())
	}
}

func TestSelfEchoDoesNotDouble(t *testing.T) {
	m, _, store := newManager(t)
	// Simulate local send -> AppendMessage and broadcast already done.
	_ = store.AppendMessage("bob", protocol.MessageRecv{
		ID: "id1", From: "alice", To: "bob", Body: "hi", TS: 1,
	})
	// Relay echoes back the message
	m.HandleRelayEvent(protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: "id1",
		From: "alice", To: "bob", Body: "hi", TS: 1,
	})
	hist, _ := store.History("bob", 10)
	if len(hist) != 1 {
		t.Errorf("expected exactly 1 stored, got %d", len(hist))
	}
}

func TestHelloRejectsBadPeer(t *testing.T) {
	m, _, _ := newManager(t)
	conn := &ipc.Conn{} // zero value — only used for SetRole/SetPeer paths
	if err := m.OnHello(conn, ipc.Hello{Op: ipc.OpHello, Role: ipc.RoleConversation, Peer: "with space"}); err == nil {
		t.Error("expected validation error")
	}
}

func TestSortUsersOnlineFirst(t *testing.T) {
	rows := []ipc.UserRow{
		{Name: "zed", Online: false},
		{Name: "bob", Online: true},
		{Name: "alice", Online: false},
	}
	sortUsers(rows)
	if rows[0].Name != "bob" || rows[1].Name != "alice" || rows[2].Name != "zed" {
		t.Errorf("sort wrong: %+v", rows)
	}
}
