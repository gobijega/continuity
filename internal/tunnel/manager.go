package tunnel

import "sync"

// Manager binds the stable overlay to whichever bearer the policy engine has
// made active. Like the routing manager it is pluggable: DryRun (the safe
// default) keeps a real in-process security association and proves, on every
// failover, that the session survives the bearer change; a production manager
// would reprogram a kernel/WireGuard-style overlay instead. The agent calls
// Rebind on each migration and reads State for the dashboard.
type Manager interface {
	Rebind(endpoint string) error
	State() State
}

// State is the observable session-continuity status published to the dashboard.
type State struct {
	Enabled     bool   `json:"enabled"`
	Overlay     string `json:"overlay"`     // stable overlay address applications bind to
	Endpoint    string `json:"endpoint"`    // bearer the overlay currently egresses through
	Cipher      string `json:"cipher"`      // AEAD protecting the session
	Sessions    int    `json:"sessions"`    // application sessions riding the overlay
	Rebinds     int    `json:"rebinds"`     // failovers ridden out without dropping the session
	Heartbeats  int    `json:"heartbeats"`  // authenticated keepalives sealed+opened after a rebind
	Established bool   `json:"established"` // handshake complete, association live
}

// DefaultOverlay is the demonstrator's stable overlay address (CGNAT space).
const DefaultOverlay = "100.64.0.1"

// DryRun is the default session-continuity manager. It performs a real X25519
// handshake against a loopback peer and, on every rebind, re-seals an
// authenticated heartbeat through the surviving security association — so the
// dashboard's numbers reflect genuine cryptography, not a mock, without touching
// the OS or the network. Safe for concurrent use.
type DryRun struct {
	mu         sync.Mutex
	overlay    string
	endpoint   string
	sessions   int
	rebinds    int
	heartbeats int
	sa         *Session
	err        error
}

// NewDryRun establishes a demo security association bound to overlay and returns
// a ready manager. sessions models how many application flows ride the overlay
// (they share its fate across a failover); it is clamped to at least 1.
func NewDryRun(overlay string, sessions int) *DryRun {
	if overlay == "" {
		overlay = DefaultOverlay
	}
	if sessions < 1 {
		sessions = 1
	}
	d := &DryRun{overlay: overlay, sessions: sessions}
	self, err1 := NewIdentity()
	peer, err2 := NewIdentity()
	if err := firstErr(err1, err2); err != nil {
		d.err = err
		return d
	}
	sa, err := NewSession(self, peer.Public(), overlay, "")
	if err != nil {
		d.err = err
		return d
	}
	d.sa = sa
	return d
}

// Rebind moves the overlay onto endpoint and proves the association is live by
// sealing and opening an authenticated heartbeat through it. The very first
// call is the initial bind (the overlay coming up on its first bearer), not a
// failover, so it runs a heartbeat but does not count as a rebind.
func (d *DryRun) Rebind(endpoint string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	if endpoint == d.endpoint {
		return nil
	}
	firstBind := d.endpoint == ""
	d.sa.Rebind(endpoint)
	frame, err := d.sa.Seal([]byte("continuity-keepalive"))
	if err != nil {
		d.err = err
		return err
	}
	if _, err := d.sa.Open(frame); err != nil {
		d.err = err
		return err
	}
	d.endpoint = endpoint
	d.heartbeats++
	if !firstBind {
		d.rebinds++ // an actual failover, not the initial bring-up
	}
	return nil
}

// State returns the current session-continuity status.
func (d *DryRun) State() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return State{
		Enabled:     true,
		Overlay:     d.overlay,
		Endpoint:    d.endpoint,
		Cipher:      CipherName,
		Sessions:    d.sessions,
		Rebinds:     d.rebinds,
		Heartbeats:  d.heartbeats,
		Established: d.sa != nil && d.err == nil,
	}
}

// Disabled is a no-op manager used when session continuity is turned off; its
// State reports Enabled=false and the API omits the tunnel view.
type Disabled struct{}

// Rebind does nothing.
func (Disabled) Rebind(string) error { return nil }

// State reports the tunnel as disabled.
func (Disabled) State() State { return State{Enabled: false} }

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
