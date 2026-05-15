package crypto

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	alice, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := Generate()
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte("hi bob, this is a confidential message")
	ct, err := alice.Encrypt(bob.PublicKey(), "msg-1", "alice", "bob", plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, "confidential") {
		t.Fatal("ciphertext leaked plaintext characters")
	}
	pt, err := bob.Decrypt(alice.PublicKey(), "msg-1", "alice", "bob", ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, plain) {
		t.Fatalf("round-trip mismatch: got %q want %q", pt, plain)
	}
}

func TestKeyDerivationIsCommutative(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()
	a, err := deriveKey(alice.priv, bob.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	b, err := deriveKey(bob.priv, alice.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("derived keys differ: %x vs %x", a, b)
	}
}

func TestAADBindingDetectsTampering(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()
	plain := []byte("hello")
	ct, _ := alice.Encrypt(bob.PublicKey(), "id1", "alice", "bob", plain)

	// Same ciphertext, different envelope — must fail.
	if _, err := bob.Decrypt(alice.PublicKey(), "id1", "alice", "carol", ct); err == nil {
		t.Error("expected decrypt failure when 'to' is changed")
	}
	if _, err := bob.Decrypt(alice.PublicKey(), "id1", "mallory", "bob", ct); err == nil {
		t.Error("expected decrypt failure when 'from' is changed")
	}
	if _, err := bob.Decrypt(alice.PublicKey(), "id2", "alice", "bob", ct); err == nil {
		t.Error("expected decrypt failure when 'id' is changed")
	}
	// Wrong sender pubkey — must fail.
	mallory, _ := Generate()
	if _, err := bob.Decrypt(mallory.PublicKey(), "id1", "alice", "bob", ct); err == nil {
		t.Error("expected decrypt failure with wrong sender pubkey")
	}
}

func TestNonceUniqueness(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		ct, err := alice.Encrypt(bob.PublicKey(), "id", "alice", "bob", []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[ct] {
			t.Fatal("repeated ciphertext — nonce reuse?")
		}
		seen[ct] = true
	}
}

func TestLoadOrCreatePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PublicKey(), second.PublicKey()) {
		t.Error("LoadOrCreate didn't reload the persisted identity")
	}
}

func TestParsePublicKey(t *testing.T) {
	id, _ := Generate()
	raw, err := ParsePublicKey(id.PublicKeyB64())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, id.PublicKey()) {
		t.Error("round-trip pubkey mismatch")
	}
	if _, err := ParsePublicKey("not-base64!!"); err == nil {
		t.Error("expected base64 error")
	}
	if _, err := ParsePublicKey("AAAA"); err == nil {
		t.Error("expected length error")
	}
}

func TestFingerprintStable(t *testing.T) {
	id, _ := Generate()
	fp1 := Fingerprint(id.PublicKey())
	fp2 := FingerprintB64(id.PublicKeyB64())
	if fp1 != fp2 {
		t.Errorf("fingerprint mismatch: %s vs %s", fp1, fp2)
	}
	if len(fp1) != 23 { // 8 bytes -> 16 hex + 7 colons
		t.Errorf("fingerprint length %d, want 23", len(fp1))
	}
}
