package conversation

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cedrx/chatd/internal/crypto"
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
	id, err := crypto.Generate()
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{
		Username:       "alice",
		Identity:       id,
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

func TestEncryptedRecvDecryptsAndStoresPlaintext(t *testing.T) {
	m, _, store := newManager(t)

	// Pretend bob is the manager's identity (newManager calls itself
	// "alice" but we can rebind for clarity here). Generate alice's
	// identity, register her pubkey via AuthOK.
	alice, err := crypto.Generate()
	if err != nil {
		t.Fatal(err)
	}
	m.HandleRelayEvent(protocol.AuthOK{
		Type: protocol.TypeAuthOK, Self: "alice",
		Users: []protocol.User{
			{Name: "alice", Online: true, PubKey: m.cfg.Identity.PublicKeyB64()},
			{Name: "bob", Online: true, PubKey: alice.PublicKeyB64()},
		},
	})

	// alice encrypts a message addressed to "alice" (the manager's
	// configured username) using the manager's pubkey.
	plaintext := []byte("e2e ping")
	myPub := m.cfg.Identity.PublicKey()
	ct, err := alice.Encrypt(myPub, "msg-7", "bob", "alice", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	m.HandleRelayEvent(protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: "msg-7",
		From: "bob", To: "alice", Body: ct, Encrypted: true,
		TS: 1,
	})
	hist, _ := store.History("bob", 10)
	if len(hist) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(hist))
	}
	if hist[0].Body != string(plaintext) {
		t.Errorf("stored body = %q, want %q", hist[0].Body, plaintext)
	}
	if hist[0].Encrypted {
		t.Error("stored entry still has Encrypted=true; should be cleared after decrypt")
	}
}

func TestEncryptedRecvFailsWithoutPubkey(t *testing.T) {
	m, _, store := newManager(t)

	// Receiving an encrypted message before any pubkey was advertised
	// for the sender should drop the message (with a log line) rather
	// than write garbage to the DB.
	m.HandleRelayEvent(protocol.MessageRecv{
		Type: protocol.TypeMessageRecv, ID: "msg-1",
		From: "stranger", To: "alice", Body: "ZmFrZQ==", Encrypted: true,
		TS: 1,
	})
	hist, _ := store.History("stranger", 10)
	if len(hist) != 0 {
		t.Errorf("expected 0 stored messages, got %d", len(hist))
	}
}

func TestRecordPeerKeyTOFUOverwrite(t *testing.T) {
	m, _, store := newManager(t)
	first, _ := crypto.Generate()
	second, _ := crypto.Generate()

	m.recordPeerKey("bob", first.PublicKeyB64())
	if got, _ := store.GetPeerKey("bob"); !bytes.Equal(got, first.PublicKey()) {
		t.Error("first key didn't persist")
	}

	// A second pubkey replaces the first (and emits a log warning we
	// don't capture here; the important behaviour is that we keep
	// accepting the peer rather than error-storming).
	m.recordPeerKey("bob", second.PublicKeyB64())
	if got, _ := store.GetPeerKey("bob"); !bytes.Equal(got, second.PublicKey()) {
		t.Error("rotated key didn't persist")
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
