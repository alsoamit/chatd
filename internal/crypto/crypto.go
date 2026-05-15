// Package crypto implements end-to-end encryption for rootchat
// messages.
//
// Threat model: the relay is honest-but-curious and the wire is hostile.
// We assume each user can keep their identity file (private X25519 key)
// confidential on their own machine. Under those assumptions:
//
//   - the relay learns who is talking to whom and the size+timing of
//     each ciphertext, but not the content;
//   - a network observer (despite TLS) learns the same;
//   - tampering with the from/to/id envelope is detected at the
//     recipient via the AEAD's additional-authenticated-data binding;
//   - replay across conversations is impossible (msg id is in the AAD,
//     and the daemon dedupes ids it has already stored).
//
// What we do NOT provide (yet):
//
//   - forward secrecy or post-compromise security (no double ratchet);
//     a stolen long-term private key decrypts every past message that
//     was ever sent to the victim from any peer whose pubkey is also
//     known. To get forward secrecy we'd add ephemeral session keys.
//   - key continuity verification beyond TOFU. Out-of-band fingerprint
//     comparison is exposed via `chat keys` / `chat verify`.
//
// Construction:
//
//   shared = X25519(my_priv, peer_pub)
//   salt   = sorted(my_pub || peer_pub)            // commutative
//   key    = HKDF-Extract+Expand(SHA-256, shared, salt, "rootchat/v1")
//   nonce  = 24 random bytes (XChaCha20-safe)
//   AAD    = msg_id || "|" || from || "|" || to
//   ct     = XChaCha20-Poly1305.Seal(key, nonce, plaintext, AAD)
//   wire   = base64(nonce || ct)
//
// Either party deriving the key gets the same value because the salt
// concat is sorted before being fed to HKDF.
package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// hkdfInfo namespaces the derived key. Bumping this string is a
// "rotate everyone's session keys" knob without rotating identity keys.
const hkdfInfo = "rootchat/v1/x25519+xchacha20poly1305/message"

// NonceSize is the XChaCha20-Poly1305 nonce length in bytes.
const NonceSize = chacha20poly1305.NonceSizeX

// Identity holds a long-term X25519 keypair.
type Identity struct {
	priv *ecdh.PrivateKey
	pub  []byte // raw 32 bytes
}

// Generate produces a fresh random identity.
func Generate() (*Identity, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{priv: priv, pub: priv.PublicKey().Bytes()}, nil
}

// LoadOrCreate reads a 32-byte raw private key from path, or creates a
// new identity and persists it (mode 0600). Parent dir is created if
// missing.
func LoadOrCreate(path string) (*Identity, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("identity %s: bad length %d (want 32)", path, len(data))
		}
		priv, err := ecdh.X25519().NewPrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("identity %s: %w", path, err)
		}
		return &Identity{priv: priv, pub: priv.PublicKey().Bytes()}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	id, err := Generate()
	if err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, id.priv.Bytes(), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return id, nil
}

// PublicKey returns a copy of the raw 32-byte X25519 public key.
func (i *Identity) PublicKey() []byte {
	out := make([]byte, len(i.pub))
	copy(out, i.pub)
	return out
}

// PublicKeyB64 is the base64-encoded public key, suitable for wire transit.
func (i *Identity) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(i.pub)
}

// Fingerprint returns a short, hex-friendly identifier for an identity:
// the first 8 bytes of SHA-256(pubkey), rendered as 16 hex chars in
// pairs. Suitable for showing to humans for out-of-band verification.
func Fingerprint(pub []byte) string {
	h := sha256.Sum256(pub)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 23) // 16 hex + 7 colons
	for i := 0; i < 8; i++ {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexdigits[h[i]>>4], hexdigits[h[i]&0x0f])
	}
	return string(out)
}

// FingerprintB64 is convenience for callers that have the b64 form.
func FingerprintB64(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "<invalid>"
	}
	return Fingerprint(raw)
}

// ParsePublicKey decodes a base64-encoded X25519 public key and
// validates length. Returns the raw 32-byte form.
func ParsePublicKey(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("pubkey base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("pubkey length %d (want 32)", len(raw))
	}
	return raw, nil
}

// deriveKey runs ECDH and HKDF to produce the 32-byte AEAD key shared
// by the two endpoints. The salt is order-independent so both sides
// derive the same key.
func deriveKey(myPriv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	peer, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("peer pubkey: %w", err)
	}
	shared, err := myPriv.ECDH(peer)
	if err != nil {
		return nil, err
	}

	myPub := myPriv.PublicKey().Bytes()
	salt := make([]byte, 0, 64)
	if bytes.Compare(myPub, peerPub) < 0 {
		salt = append(salt, myPub...)
		salt = append(salt, peerPub...)
	} else {
		salt = append(salt, peerPub...)
		salt = append(salt, myPub...)
	}
	r := hkdf.New(sha256.New, shared, salt, []byte(hkdfInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

func aad(msgID, from, to string) []byte {
	return []byte(msgID + "|" + from + "|" + to)
}

// Encrypt seals plaintext for the holder of peerPub. The message
// envelope (id, from, to) is bound into the ciphertext via AAD: a
// recipient that doesn't see the same envelope refuses to decrypt.
func (i *Identity) Encrypt(peerPub []byte, msgID, from, to string, plaintext []byte) (string, error) {
	if len(peerPub) != 32 {
		return "", fmt.Errorf("peer pubkey length %d (want 32)", len(peerPub))
	}
	key, err := deriveKey(i.priv, peerPub)
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := make([]byte, 0, NonceSize+len(plaintext)+aead.Overhead())
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plaintext, aad(msgID, from, to))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt. peerPub is the sender's pubkey; the
// envelope (id, from, to) must match what the sender used or
// authentication fails.
func (i *Identity) Decrypt(peerPub []byte, msgID, from, to, b64 string) ([]byte, error) {
	if len(peerPub) != 32 {
		return nil, fmt.Errorf("peer pubkey length %d (want 32)", len(peerPub))
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("ciphertext base64: %w", err)
	}
	if len(raw) < NonceSize+chacha20poly1305.Overhead {
		return nil, errors.New("ciphertext too short")
	}
	key, err := deriveKey(i.priv, peerPub)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := raw[:NonceSize]
	ct := raw[NonceSize:]
	pt, err := aead.Open(nil, nonce, ct, aad(msgID, from, to))
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}
