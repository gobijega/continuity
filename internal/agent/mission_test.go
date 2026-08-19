package agent

import (
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/mission"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/simulator"
)

func demoHyst() policy.Hysteresis {
	return policy.Hysteresis{
		MinImprovement: 12, FailureThreshold: 35, RecoveryThreshold: 60,
		MinDwell: 3 * time.Second, DegradationHold: 2, RecoveryHold: 2, FlapPenalty: 6 * time.Second,
	}
}

func missionAgent(sim *simulator.Sim, eng *mission.Engine) *Agent {
	return New(Options{Node: "veh", Source: NewSimSource(sim, "5g"), Mission: eng, Hyst: demoHyst()})
}

func tickN(a *Agent, base time.Time, from, n int) Snapshot {
	var s Snapshot
	for i := from; i < from+n; i++ {
		s = a.Tick(base.Add(time.Duration(i) * time.Second))
	}
	return s
}

// Test A / the core thesis (spec §7, §20): two identical network states produce
// different communications decisions because the mission changed. The bearer
// metrics never change between the two halves of this test — only the mission
// profile and state do.
func TestMissionOverrideSameNetwork(t *testing.T) {
	sim := simulator.NewDemo() // healthy: fast 5G, stable SATCOM, lossy Wi-Fi
	eng := mission.NewEngine(mission.Routine, mission.StateNormal)
	a := missionAgent(sim, eng)
	T := time.Unix(1700000000, 0)

	// Routine ops: 5G is both the network-only and the mission pick; no override.
	s := tickN(a, T, 0, 4)
	if s.Active != "5g" {
		t.Fatalf("routine active = %q, want 5g", s.Active)
	}
	if s.Mission == nil {
		t.Fatal("mission view missing")
	}
	if s.Mission.NetworkPick != "5g" || s.Mission.MissionPick != "5g" {
		t.Fatalf("routine picks net=%q miss=%q, want both 5g", s.Mission.NetworkPick, s.Mission.MissionPick)
	}
	if s.Mission.Override {
		t.Errorf("routine should not override the network recommendation")
	}

	// Escalate to Mission Critical / CRITICAL — WITHOUT touching any bearer.
	eng.Set(mission.MissionCritical, mission.StateCritical)
	s = tickN(a, T, 4, 14)

	if s.Mission.NetworkPick != "5g" {
		t.Errorf("network-only pick = %q, want 5g (network is unchanged)", s.Mission.NetworkPick)
	}
	if s.Mission.MissionPick != "satcom" {
		t.Errorf("mission-aware pick = %q, want satcom", s.Mission.MissionPick)
	}
	if !s.Mission.Override {
		t.Errorf("expected mission to override the network recommendation")
	}
	if s.Active != "satcom" {
		t.Errorf("selected bearer = %q, want satcom after the mission override", s.Active)
	}
	if s.Mission.InfluenceBand != "HIGH" && s.Mission.InfluenceBand != "DOMINANT" {
		t.Errorf("mission influence band = %q, want HIGH or DOMINANT on an override", s.Mission.InfluenceBand)
	}
}

// Test C (spec §18): a one-cycle network fluctuation must not cause a switch —
// hysteresis (the degradation-hold streak) blocks it.
func TestMissionHysteresisBlocksTransient(t *testing.T) {
	sim := simulator.NewDemo()
	eng := mission.NewEngine(mission.Routine, mission.StateNormal)
	a := missionAgent(sim, eng)
	T := time.Unix(1700000000, 0)

	tickN(a, T, 0, 3) // settle on 5G
	// A single hard-but-brief dip on 5G, cleared the next cycle.
	sim.Degrade("5g", 500, 60, 30, 0.1)
	s := a.Tick(T.Add(3 * time.Second))
	if s.Active != "5g" {
		t.Fatalf("a single degraded cycle must not switch (DegradationHold=2), active=%q", s.Active)
	}
	sim.Restore("5g")
	s = a.Tick(T.Add(4 * time.Second))
	if s.Active != "5g" {
		t.Errorf("after the dip cleared, should still hold 5g, active=%q", s.Active)
	}
}

// Test D (spec §18): a sustained, sufficiently large mission advantage triggers
// a switch. Under Emergency/CRITICAL the resilient SATCOM path wins decisively.
func TestMissionAdvantageTriggersSwitch(t *testing.T) {
	sim := simulator.NewDemo()
	eng := mission.NewEngine(mission.Emergency, mission.StateCritical)
	a := missionAgent(sim, eng)
	T := time.Unix(1700000000, 0)
	s := tickN(a, T, 0, 16)
	if s.Active != "satcom" {
		t.Errorf("emergency/critical selected bearer = %q, want satcom", s.Active)
	}
	if !s.Mission.Override {
		t.Errorf("emergency/critical over a healthy network should override to the resilient path")
	}
}

// Test F (spec §18): returning to NORMAL must not immediately oscillate the
// bearer back — recovery-hold and dwell keep it stable — but it does eventually
// fail back to the preferred link.
func TestMissionReturnToNormalNoImmediateOscillation(t *testing.T) {
	sim := simulator.NewDemo()
	eng := mission.NewEngine(mission.MissionCritical, mission.StateCritical)
	a := missionAgent(sim, eng)
	T := time.Unix(1700000000, 0)

	s := tickN(a, T, 0, 16)
	if s.Active != "satcom" {
		t.Fatalf("setup: want satcom under mission-critical, got %q", s.Active)
	}

	// Drop straight back to routine/normal. The very next cycle must NOT bounce
	// back to 5G (recovery hold + dwell absorb it).
	eng.Set(mission.Routine, mission.StateNormal)
	s = a.Tick(T.Add(16 * time.Second))
	if s.Active != "satcom" {
		t.Errorf("should not oscillate back on the first normal cycle, active=%q", s.Active)
	}

	// Given enough healthy cycles, it fails back to the preferred 5G link.
	s = tickN(a, T, 17, 12)
	if s.Active != "5g" {
		t.Errorf("should fail back to preferred 5g once recovery hold + dwell pass, active=%q", s.Active)
	}
}

// Mission awareness must not perturb the network-only (legacy) agent used by the
// existing adversarial tests: a nil mission engine yields no mission view.
func TestLegacyAgentHasNoMissionView(t *testing.T) {
	sim := simulator.NewDemo()
	a := New(Options{Node: "veh", Source: NewSimSource(sim, "5g")})
	s := a.Tick(time.Unix(1700000000, 0))
	if s.Mission != nil {
		t.Errorf("legacy agent should publish no mission view, got %+v", s.Mission)
	}
}
