package tunnel

import (
	"bytes"
	"testing"
)

func mustIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}

// Two peers that ran the X25519 handshake against each other's public keys
// derive the same key, so a frame sealed by one opens on the other.
func TestHandshakeRoundTrip(t *testing.T) {
	a, b := mustIdentity(t), mustIdentity(t)
	sa, err := NewSession(a, b.Public(), "100.64.0.1", "5g")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := NewSession(b, a.Public(), "100.64.0.1", "5g")
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("mission-critical payload")
	frame, err := sa.Seal(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sb.Open(frame)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("round-trip = %q, want %q", got, msg)
	}
}

// The heart of Sprint 9: a bearer failover is a Rebind, and the session keeps
// working afterwards — same keys, same overlay, only the underlying endpoint
// moved.
func TestRebindPreservesSession(t *testing.T) {
	a, b := mustIdentity(t), mustIdentity(t)
	sa, _ := NewSession(a, b.Public(), "100.64.0.1", "5g")
	sb, _ := NewSession(b, a.Public(), "100.64.0.1", "5g")

	if _, err := sb.Open(mustSeal(t, sa, []byte("before"))); err != nil {
		t.Fatalf("pre-rebind open: %v", err)
	}

	// Fail over 5g -> satcom on both ends.
	sa.Rebind("satcom")
	sb.Rebind("satcom")

	got, err := sb.Open(mustSeal(t, sa, []byte("after")))
	if err != nil {
		t.Fatalf("post-rebind open: %v", err)
	}
	if string(got) != "after" {
		t.Fatalf("post-rebind payload = %q, want %q", got, "after")
	}
	if sa.Overlay() != "100.64.0.1" {
		t.Errorf("overlay changed to %q; it must be stable across failover", sa.Overlay())
	}
	if sa.Endpoint() != "satcom" {
		t.Errorf("endpoint = %q, want satcom", sa.Endpoint())
	}
	if sa.Rebinds() != 1 {
		t.Errorf("rebinds = %d, want 1", sa.Rebinds())
	}
	// Rebinding to the same endpoint is a no-op.
	sa.Rebind("satcom")
	if sa.Rebinds() != 1 {
		t.Errorf("rebinds after no-op = %d, want 1", sa.Rebinds())
	}
}

func TestTamperRejected(t *testing.T) {
	a, b := mustIdentity(t), mustIdentity(t)
	sa, _ := NewSession(a, b.Public(), "100.64.0.1", "5g")
	sb, _ := NewSession(b, a.Public(), "100.64.0.1", "5g")
	frame := mustSeal(t, sa, []byte("integrity"))
	frame[len(frame)-1] ^= 0x01 // flip a ciphertext bit
	if _, err := sb.Open(frame); err == nil {
		t.Fatal("expected Open to reject a tampered frame")
	}
}

// The overlay identity is bound in as additional authenticated data, so a peer
// with the same keys but a different overlay cannot open the frame.
func TestOverlayBindingAuthenticated(t *testing.T) {
	a, b := mustIdentity(t), mustIdentity(t)
	sa, _ := NewSession(a, b.Public(), "100.64.0.1", "5g")
	sb, _ := NewSession(b, a.Public(), "10.9.9.9", "5g") // different overlay
	if _, err := sb.Open(mustSeal(t, sa, []byte("x"))); err == nil {
		t.Fatal("expected Open to fail when the overlay identity differs")
	}
}

func TestDryRunManagerContinuity(t *testing.T) {
	m := NewDryRun("100.64.0.1", 3)
	st := m.State()
	if !st.Enabled || !st.Established || st.Overlay != "100.64.0.1" || st.Sessions != 3 {
		t.Fatalf("initial state = %+v", st)
	}
	if st.Cipher != CipherName {
		t.Errorf("cipher = %q, want %q", st.Cipher, CipherName)
	}

	for _, ep := range []string{"5g", "satcom", "satcom", "wifi"} {
		if err := m.Rebind(ep); err != nil {
			t.Fatalf("Rebind(%q): %v", ep, err)
		}
	}
	st = m.State()
	if st.Endpoint != "wifi" {
		t.Errorf("endpoint = %q, want wifi", st.Endpoint)
	}
	// First bind (5g) is the initial bring-up; the duplicate satcom is a no-op;
	// so only satcom and wifi count as failovers.
	if st.Rebinds != 2 {
		t.Errorf("rebinds = %d, want 2", st.Rebinds)
	}
	if st.Heartbeats != 3 { // one per distinct endpoint, initial bind included
		t.Errorf("heartbeats = %d, want 3", st.Heartbeats)
	}
	if st.Overlay != "100.64.0.1" {
		t.Errorf("overlay drifted to %q", st.Overlay)
	}
}

func TestDisabledManager(t *testing.T) {
	var m Manager = Disabled{}
	if err := m.Rebind("5g"); err != nil {
		t.Fatalf("Disabled.Rebind: %v", err)
	}
	if m.State().Enabled {
		t.Fatal("Disabled manager must report Enabled=false")
	}
}

func mustSeal(t *testing.T, s *Session, msg []byte) []byte {
	t.Helper()
	f, err := s.Seal(msg)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return f
}
