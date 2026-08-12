package agent

import (
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/simulator"
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
