package policy

import (
	"testing"
	"time"
)

func b(name string, score float64, avail, pref bool) Scored {
	return Scored{Name: name, Score: score, Available: avail, Preferred: pref}
}

// The flagship failover story, exercised through the controller with a
// controlled clock: 5G is preferred and active; it degrades; after the
// degradation hold and dwell it migrates to SATCOM; when 5G recovers it fails
// back. Hysteresis prevents a switch on a single bad reading.
func TestFailoverAndRecovery(t *testing.T) {
	h := Hysteresis{MinImprovement: 15, FailureThreshold: 30, RecoveryThreshold: 50, MinDwell: 1 * time.Second, DegradationHold: 2}
	c := NewController(h)
	T := time.Unix(1700000000, 0)

	// t0: initial selection picks the best available (5G).
	d := c.Decide(T, []Scored{b("5g", 94, true, true), b("satcom", 72, true, false), b("wifi", 61, true, false)})
	if d.Action != Migrate || d.To != "5g" {
		t.Fatalf("t0: got %+v, want initial migrate to 5g", d)
	}

	// t1 (+2s): 5G drops to 24 but this is only the first failing cycle -> HOLD.
	d = c.Decide(T.Add(2*time.Second), []Scored{b("5g", 24, true, true), b("satcom", 74, true, false), b("wifi", 61, true, false)})
	if d.Action != Hold {
		t.Fatalf("t1: got %+v, want HOLD (degradation hold not yet met)", d)
	}

	// t2 (+4s): still failing (2nd cycle), dwell satisfied, SATCOM clearly better -> MIGRATE.
	d = c.Decide(T.Add(4*time.Second), []Scored{b("5g", 22, true, true), b("satcom", 74, true, false), b("wifi", 61, true, false)})
	if d.Action != Migrate || d.From != "5g" || d.To != "satcom" {
		t.Fatalf("t2: got %+v, want MIGRATE 5g->satcom", d)
	}

	// t3 (+6s): on SATCOM, 5G still down -> HOLD (5G below recovery threshold).
	d = c.Decide(T.Add(6*time.Second), []Scored{b("5g", 24, true, true), b("satcom", 74, true, false), b("wifi", 61, true, false)})
	if d.Action != Hold {
		t.Fatalf("t3: got %+v, want HOLD", d)
	}

	// t4 (+8s): 5G recovers to 90 -> fail back to the preferred path.
	d = c.Decide(T.Add(8*time.Second), []Scored{b("5g", 90, true, true), b("satcom", 74, true, false), b("wifi", 61, true, false)})
	if d.Action != Migrate || d.From != "satcom" || d.To != "5g" {
		t.Fatalf("t4: got %+v, want MIGRATE satcom->5g (recovery)", d)
	}
}

func TestNoFlapOnTransientBlip(t *testing.T) {
	h := Hysteresis{MinImprovement: 15, FailureThreshold: 30, RecoveryThreshold: 50, MinDwell: 0, DegradationHold: 2}
	c := NewController(h)
	T := time.Unix(1700000000, 0)
	c.Decide(T, []Scored{b("5g", 90, true, true), b("satcom", 80, true, false)})

	// One bad reading, then immediately fine again: must not switch.
	d1 := c.Decide(T.Add(time.Second), []Scored{b("5g", 20, true, true), b("satcom", 80, true, false)})
	if d1.Action != Hold {
		t.Fatalf("single blip: got %+v, want HOLD", d1)
	}
	d2 := c.Decide(T.Add(2*time.Second), []Scored{b("5g", 90, true, true), b("satcom", 80, true, false)})
	if d2.Action != Hold || c.Active() != "5g" {
		t.Fatalf("after recovery: got %+v active=%s, want HOLD on 5g", d2, c.Active())
	}
}

func TestHardLossMigratesImmediately(t *testing.T) {
	h := DefaultHysteresis() // MinDwell 10s
	c := NewController(h)
	T := time.Unix(1700000000, 0)
	c.Decide(T, []Scored{b("5g", 90, true, true), b("satcom", 70, true, false)})

	// 5G vanishes 1s later — dwell is 10s, but hard loss waives it.
	d := c.Decide(T.Add(time.Second), []Scored{b("5g", 0, false, true), b("satcom", 70, true, false)})
	if d.Action != Migrate || d.To != "satcom" || d.Reason == "" {
		t.Fatalf("hard loss: got %+v, want immediate MIGRATE to satcom", d)
	}
}

// Sprint 8: after failing away from the preferred bearer, a fail-back waits for
// the home link to test healthy for RecoveryHold consecutive cycles — one good
// reading is not enough, and a dip in between resets the streak.
func TestRecoveryHoldDelaysFailback(t *testing.T) {
	h := Hysteresis{MinImprovement: 15, FailureThreshold: 30, RecoveryThreshold: 50, MinDwell: 0, DegradationHold: 1, RecoveryHold: 3}
	c := NewController(h)
	T := time.Unix(1700000000, 0)

	c.Decide(T, []Scored{b("5g", 90, true, true), b("satcom", 70, true, false)})
	// 5G collapses; DegradationHold 1 + urgent → migrate to satcom.
	d := c.Decide(T.Add(time.Second), []Scored{b("5g", 10, true, true), b("satcom", 70, true, false)})
	if d.Action != Migrate || d.To != "satcom" {
		t.Fatalf("setup: got %+v, want MIGRATE 5g->satcom", d)
	}

	// 5G recovers, but must sustain 3 healthy cycles before we fail back.
	for i, at := range []time.Duration{2, 3} {
		d = c.Decide(T.Add(at*time.Second), []Scored{b("5g", 80, true, true), b("satcom", 70, true, false)})
		if d.Action != Hold {
			t.Fatalf("recovery cycle %d: got %+v, want HOLD (recovery hold not met)", i+1, d)
		}
	}
	// A dip on the 3rd cycle must reset the streak.
	if d = c.Decide(T.Add(4*time.Second), []Scored{b("5g", 40, true, true), b("satcom", 70, true, false)}); d.Action != Hold {
		t.Fatalf("dip: got %+v, want HOLD", d)
	}
	if c.RecoveredStreak() != 0 {
		t.Fatalf("recovered streak = %d after dip, want 0", c.RecoveredStreak())
	}
	// Now three clean cycles in a row → fail back on the third.
	c.Decide(T.Add(5*time.Second), []Scored{b("5g", 80, true, true), b("satcom", 70, true, false)})
	c.Decide(T.Add(6*time.Second), []Scored{b("5g", 80, true, true), b("satcom", 70, true, false)})
	d = c.Decide(T.Add(7*time.Second), []Scored{b("5g", 80, true, true), b("satcom", 70, true, false)})
	if d.Action != Migrate || d.From != "satcom" || d.To != "5g" {
		t.Fatalf("failback: got %+v, want MIGRATE satcom->5g after recovery hold", d)
	}
}

// Sprint 8: with no preferred bearer, two close backups could ping-pong. The
// flap penalty puts the just-left bearer in a cooldown, so a soft migration back
// onto it is suppressed until the cooldown expires.
func TestFlapPenaltySuppressesReturn(t *testing.T) {
	h := Hysteresis{MinImprovement: 10, FailureThreshold: 20, RecoveryThreshold: 50, MinDwell: 0, DegradationHold: 1, FlapPenalty: 5 * time.Second}
	c := NewController(h)
	T := time.Unix(1700000000, 0)

	c.Decide(T, []Scored{b("a", 80, true, false), b("b", 60, true, false)}) // active=a
	// b overtakes a clearly → migrate a->b; a enters cooldown until T+6s.
	d := c.Decide(T.Add(time.Second), []Scored{b("a", 70, true, false), b("b", 95, true, false)})
	if d.Action != Migrate || d.From != "a" || d.To != "b" {
		t.Fatalf("setup: got %+v, want MIGRATE a->b", d)
	}
	// a now looks better, but it is in cooldown and b is not urgent → suppressed.
	d = c.Decide(T.Add(2*time.Second), []Scored{b("a", 95, true, false), b("b", 70, true, false)})
	if d.Action != Hold {
		t.Fatalf("cooldown: got %+v, want HOLD (flap-suppressed)", d)
	}
	if c.Suppressed() != 1 {
		t.Fatalf("suppressed = %d, want 1", c.Suppressed())
	}
	// Once the cooldown expires, the sustained better path wins.
	d = c.Decide(T.Add(7*time.Second), []Scored{b("a", 95, true, false), b("b", 70, true, false)})
	if d.Action != Migrate || d.From != "b" || d.To != "a" {
		t.Fatalf("post-cooldown: got %+v, want MIGRATE b->a", d)
	}
}

// Sprint 8: the flap penalty must never trap traffic on a failing link — an
// urgently-bad active bearer bypasses the cooldown.
func TestFlapPenaltyBypassedWhenUrgent(t *testing.T) {
	h := Hysteresis{MinImprovement: 10, FailureThreshold: 40, RecoveryThreshold: 50, MinDwell: 0, DegradationHold: 1, FlapPenalty: 30 * time.Second}
	c := NewController(h)
	T := time.Unix(1700000000, 0)

	c.Decide(T, []Scored{b("a", 80, true, false), b("b", 60, true, false)})                  // active=a
	c.Decide(T.Add(time.Second), []Scored{b("a", 70, true, false), b("b", 95, true, false)}) // a->b, a in cooldown
	// b craters below the failure threshold: urgent → cooldown on a is waived.
	d := c.Decide(T.Add(2*time.Second), []Scored{b("a", 95, true, false), b("b", 15, true, false)})
	if d.Action != Migrate || d.To != "a" {
		t.Fatalf("urgent bypass: got %+v, want MIGRATE b->a despite cooldown", d)
	}
}
