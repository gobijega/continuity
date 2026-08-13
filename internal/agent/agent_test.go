package agent

import (
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/simulator"
	"github.com/gobijega/continuity/internal/tunnel"
)

// End-to-end through the orchestrator over the simulator: start resilient on
// the preferred 5G link, degrade it, migrate to SATCOM, restore 5G, fail back.
func TestAgentFailoverWithSim(t *testing.T) {
	sim := simulator.NewDemo()
	a := New(Options{
		Node:    "vehicle-01",
		Profile: "default",
		Source:  NewSimSource(sim, "5g"),
		Hyst:    policy.Hysteresis{MinImprovement: 12, FailureThreshold: 35, RecoveryThreshold: 60, MinDwell: 0, DegradationHold: 1},
	})
	T := time.Unix(1700000000, 0)

	s := a.Tick(T)
	if s.Active != "5g" {
		t.Fatalf("initial active = %q, want 5g", s.Active)
	}
	if s.Status != "RESILIENT" {
		t.Errorf("status = %q, want RESILIENT", s.Status)
	}

	// Degrade 5G hard — it should migrate to the best healthy backup (SATCOM).
	sim.Degrade("5g", 400, 50, 25, 0.1)
	s = a.Tick(T.Add(1 * time.Second))
	if s.Active != "satcom" {
		t.Fatalf("after degrade, active = %q, want satcom", s.Active)
	}

	// Restore 5G — it should fail back to the preferred home bearer.
	sim.Restore("5g")
	s = a.Tick(T.Add(2 * time.Second))
	if s.Active != "5g" {
		t.Fatalf("after restore, active = %q, want 5g (fail back)", s.Active)
	}

	// The migrations should be visible in the event log.
	var migrations int
	for _, e := range s.Events {
		if e.Type == "MIGRATE" {
			migrations++
		}
	}
	if migrations < 2 {
		t.Errorf("expected >=2 MIGRATE events in the log, got %d", migrations)
	}
}

// Sprint 9: the encrypted overlay must follow the active bearer across a
// failover while keeping its stable address, and record the preserved session.
func TestSessionContinuityAcrossFailover(t *testing.T) {
	sim := simulator.NewDemo()
	a := New(Options{
		Node:   "vehicle-01",
		Source: NewSimSource(sim, "5g"),
		Hyst:   policy.Hysteresis{MinImprovement: 12, FailureThreshold: 35, RecoveryThreshold: 60, MinDwell: 0, DegradationHold: 1},
	})
	T := time.Unix(1700000000, 0)

	s := a.Tick(T)
	if s.Continuity == nil || !s.Continuity.Enabled {
		t.Fatalf("expected enabled continuity state, got %+v", s.Continuity)
	}
	if s.Continuity.Endpoint != "5g" {
		t.Errorf("initial overlay endpoint = %q, want 5g", s.Continuity.Endpoint)
	}
	if s.Continuity.Rebinds != 0 {
		t.Errorf("rebinds = %d before any failover, want 0", s.Continuity.Rebinds)
	}
	overlay := s.Continuity.Overlay

	// Fail 5G over to SATCOM — the overlay follows, counts a rebind, and keeps
	// its stable address.
	sim.Degrade("5g", 400, 50, 25, 0.1)
	s = a.Tick(T.Add(time.Second))
	if s.Active != "satcom" {
		t.Fatalf("active = %q, want satcom", s.Active)
	}
	if s.Continuity.Endpoint != "satcom" {
		t.Errorf("overlay endpoint = %q, want satcom after failover", s.Continuity.Endpoint)
	}
	if s.Continuity.Rebinds != 1 {
		t.Errorf("rebinds = %d, want 1 after one failover", s.Continuity.Rebinds)
	}
	if s.Continuity.Overlay != overlay {
		t.Errorf("overlay address changed to %q; it must stay stable", s.Continuity.Overlay)
	}

	var rebinds int
	for _, e := range s.Events {
		if e.Type == "REBIND" {
			rebinds++
		}
	}
	if rebinds < 1 {
		t.Errorf("expected a REBIND event after failover, got %d", rebinds)
	}
}

func TestContinuityDisabled(t *testing.T) {
	sim := simulator.NewDemo()
	a := New(Options{Source: NewSimSource(sim, "5g"), Tunnel: tunnel.Disabled{}})
	s := a.Tick(time.Unix(1700000000, 0))
	if s.Continuity != nil {
		t.Errorf("continuity = %+v, want nil when disabled", s.Continuity)
	}
}

func TestActiveRoleAssigned(t *testing.T) {
	sim := simulator.NewDemo()
	a := New(Options{Source: NewSimSource(sim, "5g")})
	s := a.Tick(time.Unix(1700000000, 0))
	var active int
	for _, b := range s.Bearers {
		if b.Role == "ACTIVE" {
			active++
			if b.Name != s.Active {
				t.Errorf("ACTIVE role on %q but snapshot active is %q", b.Name, s.Active)
			}
		}
	}
	if active != 1 {
		t.Errorf("expected exactly one ACTIVE bearer, got %d", active)
	}
}
