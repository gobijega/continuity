package mission

import "testing"

func outcomeOf(decs []TrafficDecision, id string) Outcome {
	for _, d := range decs {
		if d.ID == id {
			return d.Outcome
		}
	}
	return ""
}

// Test B (spec §18): critical traffic is prioritised over bulk traffic. Under a
// CRITICAL state, command/safety/telemetry are prioritised while bulk is
// deferred and background sync is suspended — matching the spec §5 worked
// example exactly.
func TestTrafficPolicyPrioritisesCriticalOverBulk(t *testing.T) {
	p := TrafficPolicy(StateCritical)
	if outcomeOf(p, "c2") != Prioritise || outcomeOf(p, "safety") != Prioritise || outcomeOf(p, "telemetry") != Prioritise {
		t.Errorf("critical/safety/telemetry should be PRIORITISE under CRITICAL: %+v", p)
	}
	if outcomeOf(p, "voice") != Normal {
		t.Errorf("voice should be NORMAL under CRITICAL, got %v", outcomeOf(p, "voice"))
	}
	if outcomeOf(p, "video") != Throttle {
		t.Errorf("video should be THROTTLE under CRITICAL, got %v", outcomeOf(p, "video"))
	}
	if outcomeOf(p, "bulk") != Defer {
		t.Errorf("bulk should be DEFER under CRITICAL, got %v", outcomeOf(p, "bulk"))
	}
	if outcomeOf(p, "bgsync") != Suspend {
		t.Errorf("background sync should be SUSPEND under CRITICAL, got %v", outcomeOf(p, "bgsync"))
	}
}

// Traffic policy escalates monotonically: nothing is throttled at NORMAL, and
// pressure on lower-value traffic only ever increases as the state rises.
func TestTrafficPolicyEscalatesMonotonically(t *testing.T) {
	for _, d := range TrafficPolicy(StateNormal) {
		if d.Outcome != Normal {
			t.Errorf("NORMAL state should carry all traffic NORMAL, got %s=%s", d.ID, d.Outcome)
		}
	}
	rank := map[Outcome]int{Prioritise: -1, Normal: 0, Throttle: 1, Defer: 2, Suspend: 3}
	states := []State{StateNormal, StateElevated, StateDegraded, StateCritical}
	for _, id := range []string{"opdata", "video", "bulk", "bgsync"} {
		prev := -100
		for _, st := range states {
			cur := rank[outcomeOf(TrafficPolicy(st), id)]
			if cur < prev {
				t.Errorf("%s de-escalated from rank %d to %d at state %s", id, prev, cur, st)
			}
			prev = cur
		}
	}
}

// Test E (spec §18): a mission-state transition changes the scoring weights —
// resilience and availability gain weight while bandwidth loses it, even within
// a single profile.
func TestStateTransitionChangesWeights(t *testing.T) {
	wNormal := MissionWeights(MissionCritical, StateNormal, DominantClass(StateNormal)).Normalized()
	wCritical := MissionWeights(MissionCritical, StateCritical, DominantClass(StateCritical)).Normalized()

	if !(wCritical.Resilience > wNormal.Resilience) {
		t.Errorf("resilience weight should rise NORMAL->CRITICAL: %.3f -> %.3f", wNormal.Resilience, wCritical.Resilience)
	}
	if !(wCritical.Availability > wNormal.Availability) {
		t.Errorf("availability weight should rise NORMAL->CRITICAL: %.3f -> %.3f", wNormal.Availability, wCritical.Availability)
	}
	if !(wCritical.Bandwidth < wNormal.Bandwidth) {
		t.Errorf("bandwidth weight should fall NORMAL->CRITICAL: %.3f -> %.3f", wNormal.Bandwidth, wCritical.Bandwidth)
	}
}

// Test A at the weight level: two different profiles produce different weightings
// for the same state, so the same telemetry can rank differently.
func TestProfilesProduceDifferentWeights(t *testing.T) {
	routine := MissionWeights(Routine, StateNormal, DominantClass(StateNormal)).Normalized()
	assured := MissionWeights(MissionCritical, StateNormal, DominantClass(StateNormal)).Normalized()
	if routine.Bandwidth <= assured.Bandwidth {
		t.Errorf("routine should weight bandwidth above mission-critical: %.3f vs %.3f", routine.Bandwidth, assured.Bandwidth)
	}
	if assured.Resilience <= routine.Resilience {
		t.Errorf("mission-critical should weight resilience above routine: %.3f vs %.3f", assured.Resilience, routine.Resilience)
	}
}

func TestNetworkOnlyWeightsHaveNoResilience(t *testing.T) {
	if NetworkOnlyWeights().Resilience != 0 {
		t.Errorf("the network-only baseline must not weight structural resilience")
	}
}

func TestClassifyBearer(t *testing.T) {
	cases := []struct {
		name, kind string
		want       BearerType
	}{
		{"satcom", "tunnel", Satellite},
		{"5g", "cellular", Cellular},
		{"wifi", "wifi", WiFi},
		{"eth0", "ethernet", Wired},
		{"tac-radio", "other", Radio},
	}
	for _, c := range cases {
		if got := ClassifyBearer(c.name, c.kind); got != c.want {
			t.Errorf("ClassifyBearer(%q,%q) = %q, want %q", c.name, c.kind, got, c.want)
		}
	}
}

// Contested policy penalises high-emission, infrastructure-dependent bearers
// (cellular) and favours low-observable ones (wired) — the "bearer-risk" factor.
func TestContestedSuitabilityPenalisesCellular(t *testing.T) {
	cell := Suitability(Contested, StateDegraded, Cellular)
	wired := Suitability(Contested, StateDegraded, Wired)
	if cell >= 1.0 {
		t.Errorf("contested should penalise cellular (<1.0), got %.2f", cell)
	}
	if wired <= cell {
		t.Errorf("contested should favour wired over cellular, got wired=%.2f cell=%.2f", wired, cell)
	}
}

// The influence metric is deterministic and reflects reality: routine ops barely
// shift the decision (LOW); an actual override reads HIGH or DOMINANT.
func TestInfluenceMetric(t *testing.T) {
	routineW := MissionWeights(Routine, StateNormal, DominantClass(StateNormal))
	net := NetworkOnlyWeights()
	neutralSuit := map[string]float64{"a": 1.0, "b": 1.0}
	pct, band := Influence(routineW, net, neutralSuit, false)
	if band != "LOW" && band != "MODERATE" {
		t.Errorf("routine influence should be LOW/MODERATE, got %s (%.1f)", band, pct)
	}

	critW := MissionWeights(MissionCritical, StateCritical, DominantClass(StateCritical))
	skewSuit := map[string]float64{"sat": 1.19, "cell": 0.9}
	pct2, band2 := Influence(critW, net, skewSuit, true)
	if band2 != "HIGH" && band2 != "DOMINANT" {
		t.Errorf("an override should read HIGH/DOMINANT, got %s (%.1f)", band2, pct2)
	}
	// Determinism.
	pct3, _ := Influence(critW, net, skewSuit, true)
	if pct2 != pct3 {
		t.Errorf("influence must be deterministic: %.2f vs %.2f", pct2, pct3)
	}
}

func TestScenariosPresent(t *testing.T) {
	want := []string{"routine-mobility", "coverage-loss", "mission-priority-override", "critical-traffic-event", "contested"}
	for _, id := range want {
		s, ok := ScenarioByID(id)
		if !ok {
			t.Errorf("missing scenario %q", id)
			continue
		}
		if s.Profile == "" || s.State == "" || s.Caption == "" {
			t.Errorf("scenario %q incomplete: %+v", id, s)
		}
	}
	if s, _ := ScenarioByID("mission-priority-override"); s.Profile != MissionCritical || s.State != StateCritical {
		t.Errorf("mission-priority-override should be MISSION_CRITICAL/CRITICAL")
	}
}
