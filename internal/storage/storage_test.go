package storage

import (
	"path/filepath"
	"testing"

	"github.com/cedrx/chatd/internal/protocol"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAppendAndHistory(t *testing.T) {
	s := newStore(t)
	for i := int64(0); i < 5; i++ {
		_ = s.AppendMessage("alice", protocol.MessageRecv{
			Type: protocol.TypeMessageRecv,
			ID:   "m" + string(rune('0'+i)),
			From: "alice", To: "self", Body: "hi", TS: i + 1,
		})
	}
	got, err := s.History("alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d", len(got))
	}
	for i := 0; i < 5; i++ {
		if got[i].TS != int64(i+1) {
			t.Errorf("order wrong at %d: %+v", i, got[i])
		}
	}

	got2, _ := s.History("alice", 2)
	if len(got2) != 2 || got2[0].TS != 4 || got2[1].TS != 5 {
		t.Errorf("limit slice wrong: %+v", got2)
	}
}

func TestUnreadCounters(t *testing.T) {
	s := newStore(t)
	if n, _ := s.Unread("alice"); n != 0 {
		t.Errorf("expected 0 got %d", n)
	}
	for i := 0; i < 3; i++ {
		_, _ = s.IncrementUnread("alice")
	}
	if n, _ := s.Unread("alice"); n != 3 {
		t.Errorf("expected 3 got %d", n)
	}
	all, _ := s.AllUnread()
	if all["alice"] != 3 {
		t.Errorf("AllUnread wrong: %v", all)
	}
	_ = s.ClearUnread("alice")
	if n, _ := s.Unread("alice"); n != 0 {
		t.Errorf("clear failed")
	}
}

func TestMergeHistoryIdempotent(t *testing.T) {
	s := newStore(t)
	msgs := []protocol.MessageRecv{
		{Type: protocol.TypeMessageRecv, ID: "x", TS: 1, From: "a", To: "b", Body: "1"},
		{Type: protocol.TypeMessageRecv, ID: "y", TS: 2, From: "a", To: "b", Body: "2"},
	}
	if err := s.MergeHistory("a", msgs); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeHistory("a", msgs); err != nil {
		t.Fatal(err)
	}
	got, _ := s.History("a", 100)
	if len(got) != 2 {
		t.Errorf("expected 2 got %d", len(got))
	}
}

func TestPeers(t *testing.T) {
	s := newStore(t)
	_ = s.AppendMessage("alice", protocol.MessageRecv{ID: "1", TS: 1})
	_ = s.AppendMessage("bob", protocol.MessageRecv{ID: "2", TS: 2})
	peers, _ := s.Peers()
	if len(peers) != 2 {
		t.Errorf("got %v", peers)
	}
}
