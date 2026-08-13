// Package tunnel provides Continuity's session-continuity layer (spec §13,
// Sprint 9): a stable, encrypted overlay that keeps application sessions alive
// across a bearer failover.
//
// The idea is the same one WireGuard-style overlays use. Applications bind to a
// stable overlay address that never changes. Underneath, the agent may move
// traffic from 5G to SATCOM to Wi-Fi as links degrade — but because the
// security association (the negotiated keys and the overlay identity) is
// independent of the physical bearer, a failover is just a Rebind: the same
// encrypted session simply starts egressing through a different interface. TCP
// and application sessions ride over the overlay and never see the switch.
//
// The cryptography is real and dependency-free (Go standard library only): an
// X25519 ECDH handshake derives a shared secret, SHA-256 expands it into a
// 256-bit key, and AES-256-GCM seals every frame with the overlay address bound
// in as additional authenticated data. Binding the overlay — not the bearer —
// into the AEAD is what makes a bearer change transparent while still
// authenticating the session's identity.
package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
)

// CipherName identifies the AEAD in use, for display and audit.
const CipherName = "AES-256-GCM"

// nonceLen is the AES-GCM standard nonce size.
const nonceLen = 12

// Identity is a peer's long-lived X25519 key pair. The public half is exchanged
// during the handshake; the private half never leaves the node.
type Identity struct {
	priv *ecdh.PrivateKey
}

// NewIdentity generates a fresh X25519 identity.
func NewIdentity() (*Identity, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{priv: priv}, nil
}

// Public returns the identity's public key bytes, safe to send to a peer.
func (id *Identity) Public() []byte { return id.priv.PublicKey().Bytes() }

// Session is an established, encrypted security association bound to a stable
// overlay address. Its keys and identity are independent of the underlying
// bearer, so Rebind can move it to a new interface without dropping it. A
// Session is safe for concurrent use.
type Session struct {
	aead    cipher.AEAD
	overlay string // stable overlay address bound as AEAD additional data

	mu       sync.Mutex
	endpoint string // current underlying bearer endpoint (mutable across failover)
	rebinds  int
	sealed   int
	opened   int
}

// NewSession completes the handshake: it derives the shared key from our
// identity and the peer's public key, then binds the association to a stable
// overlay address and an initial bearer endpoint. Both peers, running this with
// each other's public keys, derive the identical key (X25519 ECDH is
// symmetric), so a frame sealed by one opens on the other.
func NewSession(self *Identity, peerPublic []byte, overlay, endpoint string) (*Session, error) {
	if self == nil {
		return nil, errors.New("tunnel: nil identity")
	}
	peerPub, err := ecdh.X25519().NewPublicKey(peerPublic)
	if err != nil {
		return nil, err
	}
	shared, err := self.priv.ECDH(peerPub)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(shared) // 256-bit key derivation from the ECDH secret
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Session{aead: aead, overlay: overlay, endpoint: endpoint}, nil
}

// Seal encrypts and authenticates a frame, returning nonce||ciphertext. The
// overlay address is bound in as additional data, so a frame is cryptographically
// tied to the session identity but not to any particular bearer.
func (s *Session) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := s.aead.Seal(nonce, nonce, plaintext, []byte(s.overlay))
	s.mu.Lock()
	s.sealed++
	s.mu.Unlock()
	return out, nil
}

// Open authenticates and decrypts a frame produced by Seal. A tampered frame,
// or one bound to a different overlay identity, fails.
func (s *Session) Open(frame []byte) ([]byte, error) {
	if len(frame) < nonceLen {
		return nil, errors.New("tunnel: short frame")
	}
	nonce, ct := frame[:nonceLen], frame[nonceLen:]
	pt, err := s.aead.Open(nil, nonce, ct, []byte(s.overlay))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
	return pt, nil
}

// Rebind moves the session onto a new bearer endpoint. The keys, overlay
// identity and frame counters are untouched — this is what lets a session
// survive a failover. Rebinding to the current endpoint is a no-op.
func (s *Session) Rebind(endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if endpoint == s.endpoint {
		return
	}
	s.endpoint = endpoint
	s.rebinds++
}

// Overlay returns the stable overlay address (never changes over the session's
// life).
func (s *Session) Overlay() string { return s.overlay }

// Endpoint returns the bearer endpoint the session is currently pinned to.
func (s *Session) Endpoint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpoint
}

// Rebinds returns how many times the session has moved bearer without dropping.
func (s *Session) Rebinds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebinds
}

// Frames returns the number of frames sealed and opened over the session's life.
func (s *Session) Frames() (sealed, opened int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sealed, s.opened
}
