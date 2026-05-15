// Package storage persists conversations on disk using bbolt. Buckets:
//
//	conv/<peer>     -> messages keyed by ts_ns/<id> (sortable)
//	unread/         -> peer -> uint64 unread count (big-endian)
//	meta/           -> miscellaneous singletons (e.g. "self")
//
// All exported methods are safe for concurrent use; bbolt serialises
// internally.
package storage

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cedrx/chatd/internal/protocol"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketConv     = []byte("conv")
	bucketUnread   = []byte("unread")
	bucketMeta     = []byte("meta")
	bucketPeerKeys = []byte("peer_keys") // peer username -> raw 32-byte X25519 pubkey
)

// Store is the on-disk persistence handle.
type Store struct {
	mu sync.Mutex
	db *bolt.DB
}

// Open opens or creates the bolt database at path.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("storage open: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketConv, bucketUnread, bucketMeta, bucketPeerKeys} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database file.
func (s *Store) Close() error { return s.db.Close() }

// keyFor builds the per-message key. Layout: 8-byte big-endian
// timestamp (ms) followed by the message id, so iteration order is
// chronological even with duplicate timestamps.
func keyFor(m protocol.MessageRecv) []byte {
	buf := make([]byte, 8+len(m.ID))
	binary.BigEndian.PutUint64(buf[:8], uint64(m.TS))
	copy(buf[8:], m.ID)
	return buf
}

// AppendMessage stores msg under conv/<peer>. peer is the conversation
// partner (always the *other* user, regardless of direction).
func (s *Store) AppendMessage(peer string, m protocol.MessageRecv) error {
	if peer == "" {
		return errors.New("storage: peer required")
	}
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bucketConv)
		sub, err := root.CreateBucketIfNotExists([]byte(peer))
		if err != nil {
			return err
		}
		return sub.Put(keyFor(m), val)
	})
}

// History returns up to limit most-recent messages for the conversation
// with peer, oldest-first.
func (s *Store) History(peer string, limit int) ([]protocol.MessageRecv, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []protocol.MessageRecv
	err := s.db.View(func(tx *bolt.Tx) error {
		sub := tx.Bucket(bucketConv).Bucket([]byte(peer))
		if sub == nil {
			return nil
		}
		c := sub.Cursor()
		// Walk from the newest backwards into a buffer, then reverse.
		count := 0
		for k, v := c.Last(); k != nil && count < limit; k, v = c.Prev() {
			var m protocol.MessageRecv
			if err := json.Unmarshal(v, &m); err == nil {
				out = append(out, m)
				count++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// MergeHistory inserts a slice of messages from the relay's history
// response. Existing keys (same ts+id) are overwritten — messages are
// idempotent.
func (s *Store) MergeHistory(peer string, msgs []protocol.MessageRecv) error {
	if peer == "" {
		return errors.New("storage: peer required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(bucketConv)
		sub, err := root.CreateBucketIfNotExists([]byte(peer))
		if err != nil {
			return err
		}
		for _, m := range msgs {
			val, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if err := sub.Put(keyFor(m), val); err != nil {
				return err
			}
		}
		return nil
	})
}

// IncrementUnread bumps the unread counter for peer. The new count is
// returned so the caller can broadcast it.
func (s *Store) IncrementUnread(peer string) (uint64, error) {
	var n uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUnread)
		cur := b.Get([]byte(peer))
		if len(cur) == 8 {
			n = binary.BigEndian.Uint64(cur)
		}
		n++
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, n)
		return b.Put([]byte(peer), buf)
	})
	return n, err
}

// ClearUnread resets the unread counter for peer.
func (s *Store) ClearUnread(peer string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUnread).Delete([]byte(peer))
	})
}

// Unread returns the current count for peer (0 if missing).
func (s *Store) Unread(peer string) (uint64, error) {
	var n uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketUnread).Get([]byte(peer))
		if len(v) == 8 {
			n = binary.BigEndian.Uint64(v)
		}
		return nil
	})
	return n, err
}

// AllUnread snapshots the entire unread map.
func (s *Store) AllUnread() (map[string]uint64, error) {
	out := map[string]uint64{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUnread).ForEach(func(k, v []byte) error {
			if len(v) == 8 {
				out[string(k)] = binary.BigEndian.Uint64(v)
			}
			return nil
		})
	})
	return out, err
}

// SetPeerKey records peer's raw 32-byte X25519 pubkey. Overwrites any
// existing entry. Caller is responsible for surfacing key-change
// warnings before calling — by the time we get here we trust the key.
func (s *Store) SetPeerKey(peer string, pubkey []byte) error {
	if peer == "" || len(pubkey) != 32 {
		return errors.New("storage: SetPeerKey: invalid arguments")
	}
	cp := make([]byte, len(pubkey))
	copy(cp, pubkey)
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPeerKeys).Put([]byte(peer), cp)
	})
}

// GetPeerKey returns the cached pubkey for peer, or (nil, nil) if none.
func (s *Store) GetPeerKey(peer string) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketPeerKeys).Get([]byte(peer))
		if len(v) == 32 {
			out = make([]byte, 32)
			copy(out, v)
		}
		return nil
	})
	return out, err
}

// AllPeerKeys snapshots every cached pubkey. Useful for `chat keys`.
func (s *Store) AllPeerKeys() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPeerKeys).ForEach(func(k, v []byte) error {
			if len(v) != 32 {
				return nil
			}
			cp := make([]byte, 32)
			copy(cp, v)
			out[string(k)] = cp
			return nil
		})
	})
	return out, err
}

// Peers returns every peer for whom we have at least one stored message.
func (s *Store) Peers() ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketConv).ForEach(func(k, _ []byte) error {
			out = append(out, string(k))
			return nil
		})
	})
	return out, err
}
