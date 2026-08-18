// Package mission is the Continuity Mission Context Engine (spec §2): the layer
// that makes bearer selection mission-aware.
//
//	Networks optimise for connectivity. Continuity optimises for the mission.
//
// A conventional bearer selector ranks links on live network metrics alone —
// latency, loss, jitter, throughput, availability, cost. The Mission Context
// Engine adds *why communications matter right now*. From a selected mission
// profile and operational state it derives the concrete inputs the decision
// engine consumes:
//
//   - a network-component weighting (mission-derived performance weighting),
//   - a per-bearer suitability multiplier (mission-derived bearer weighting),
//   - switching-threshold modifiers (mission-modulated hysteresis),
//   - a traffic-class policy (which traffic is prioritised / throttled / …),
//   - and human-readable operational constraints.
//
// The engine is a modular simulator: it holds the current profile and state and
// produces an immutable Context each cycle. A real external mission-context
// source (a battle-management feed, a mission-planning system) could replace it
// behind the same Context interface without touching the decision engine.
//
// Everything here is SIMULATED policy for a software demonstrator. It models
// mission-aware bearer selection; it does not encode classified threat models,
// certified doctrine, or validated operational suitability.
package mission

import (
	"sync"

	"github.com/gobijega/continuity/internal/scoring"
)

// Profile is a simulated mission profile — the standing objective that reshapes
// how bearers are weighed (spec §3).
type Profile string

const (
	Routine         Profile = "ROUTINE"          // Routine Operations
	MissionCritical Profile = "MISSION_CRITICAL" // Mission Critical
	Emergency       Profile = "EMERGENCY"        // Emergency
	Contested       Profile = "CONTESTED"        // Contested / Degraded Environment
)

// State is the operational mission state, distinct from the profile (spec §4).
type State string

const (
	StateNormal   State = "NORMAL"
	StateElevated State = "ELEVATED"
	StateDegraded State = "DEGRADED"
	StateCritical State = "CRITICAL"
)

// ProfileInfo describes a profile for the UI/API.
type ProfileInfo struct {
	ID        Profile `json:"id"`
	Name      string  `json:"name"`
	Objective string  `json:"objective"`
}

// Profiles returns the selectable mission profiles with display metadata.
func Profiles() []ProfileInfo {
	return []ProfileInfo{
		{Routine, "Routine Operations", "Maximise useful throughput; favour economical, high-capacity connectivity; minimise unnecessary switching."},
		{MissionCritical, "Mission Critical", "Preserve operational continuity; favour reliability and resilience over raw throughput; protect critical traffic."},
		{Emergency, "Emergency", "Maximise the probability that critical messages remain deliverable; aggressively deprioritise non-critical traffic."},
		{Contested, "Contested / Degraded", "Maintain communications under simulated degraded conditions; penalise bearers by operational policy; prioritise diversity and resilience."},
	}
}

// States returns the operational states in ascending order of pressure.
func States() []State { return []State{StateNormal, StateElevated, StateDegraded, StateCritical} }

// ValidProfile reports whether p is a known profile.
func ValidProfile(p Profile) bool {
	switch p {
	case Routine, MissionCritical, Emergency, Contested:
		return true
	}
	return false
}

// ValidState reports whether s is a known state.
func ValidState(s State) bool {
	switch s {
	case StateNormal, StateElevated, StateDegraded, StateCritical:
		return true
	}
	return false
}

// HystMod carries mission-derived modifiers to the hysteresis parameters (spec
// §12). Mission awareness augments hysteresis rather than removing it: NORMAL
// raises the switching threshold and dwell so routine ops never churn on noise;
// CRITICAL lowers them so a significant mission advantage can be taken quickly —
// but the anti-oscillation machinery (streaks, dwell, flap penalty) still runs.
type HystMod struct {
	MinImprovementScale float64 `json:"min_improvement_scale"`
	MinDwellScale       float64 `json:"min_dwell_scale"`
	Note                string  `json:"note"`
}

func hystModFor(st State) HystMod {
	switch st {
	case StateCritical:
		return HystMod{0.55, 0.5, "CRITICAL: switching threshold lowered — a significant mission advantage is taken quickly."}
	case StateDegraded:
		return HystMod{0.8, 0.75, "DEGRADED: switching threshold eased to favour the more resilient path."}
	case StateElevated:
		return HystMod{1.0, 1.0, "ELEVATED: standard switching thresholds; reliability weighting increased."}
	default:
		return HystMod{1.35, 1.4, "NORMAL: switching threshold raised — hold the current path unless clearly beaten."}
	}
}

// Context is the immutable mission context for one decision cycle. It is what
// the Mission Context Engine hands the decision engine, and what the dashboard
// renders as the mission layer.
type Context struct {
	Profile        Profile            `json:"profile"`
	ProfileName    string             `json:"profile_name"`
	State          State              `json:"state"`
	Objective      string             `json:"objective"`
	ActiveClass    TrafficClass       `json:"active_class"`
	Weights        scoring.Weights    `json:"weights"`         // mission-aware network weighting
	NetworkWeights scoring.Weights    `json:"network_weights"` // conventional network-only baseline
	Constraints    []string           `json:"constraints"`
	Traffic        []TrafficDecision  `json:"traffic"`
	Hysteresis     HystMod            `json:"hysteresis"`
	Thresh         scoring.Thresholds `json:"-"`
}

// Engine holds the live mission profile and state and produces a Context. Safe
// for concurrent use: the API mutates it while the control loop reads it.
type Engine struct {
	mu      sync.RWMutex
	profile Profile
	state   State
}

// NewEngine builds an engine, defaulting to Routine / NORMAL.
func NewEngine(p Profile, st State) *Engine {
	if !ValidProfile(p) {
		p = Routine
	}
	if !ValidState(st) {
		st = StateNormal
	}
	return &Engine{profile: p, state: st}
}

// Profile returns the current mission profile.
func (e *Engine) Profile() Profile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.profile
}

// State returns the current operational state.
func (e *Engine) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// SetProfile updates the mission profile; returns false for an unknown profile.
func (e *Engine) SetProfile(p Profile) bool {
	if !ValidProfile(p) {
		return false
	}
	e.mu.Lock()
	e.profile = p
	e.mu.Unlock()
	return true
}

// SetState updates the operational state; returns false for an unknown state.
func (e *Engine) SetState(st State) bool {
	if !ValidState(st) {
		return false
	}
	e.mu.Lock()
	e.state = st
	e.mu.Unlock()
	return true
}

// Set updates profile and state together (used by scenario presets).
func (e *Engine) Set(p Profile, st State) {
	e.mu.Lock()
	if ValidProfile(p) {
		e.profile = p
	}
	if ValidState(st) {
		e.state = st
	}
	e.mu.Unlock()
}

// Context assembles the current mission context.
func (e *Engine) Context() Context {
	p, st := e.Profile(), e.State()
	dominant := DominantClass(st)
	return Context{
		Profile:        p,
		ProfileName:    profileName(p),
		State:          st,
		Objective:      objective(p),
		ActiveClass:    dominant,
		Weights:        MissionWeights(p, st, dominant),
		NetworkWeights: NetworkOnlyWeights(),
		Constraints:    constraints(p, st),
		Traffic:        TrafficPolicy(st),
		Hysteresis:     hystModFor(st),
		Thresh:         Thresholds(),
	}
}

func profileName(p Profile) string {
	for _, pi := range Profiles() {
		if pi.ID == p {
			return pi.Name
		}
	}
	return string(p)
}

func objective(p Profile) string {
	for _, pi := range Profiles() {
		if pi.ID == p {
			return pi.Objective
		}
	}
	return ""
}

// constraints returns the human-readable operational constraints in force for a
// profile + state — the "mission-derived policy constraints" the engine exposes.
func constraints(p Profile, st State) []string {
	var out []string
	switch p {
	case Routine:
		out = append(out, "Bulk telemetry and video are permitted.", "Prefer high-capacity, economical bearers.", "Minimise unnecessary switching.")
	case MissionCritical:
		out = append(out, "Preserve command traffic ahead of bulk data.", "Reliability and continuity outrank throughput.", "Favour stable, always-available bearers.")
	case Emergency:
		out = append(out, "Non-critical traffic may be throttled or deferred.", "Prefer the bearer most likely to preserve critical comms, even at lower bandwidth.", "Cost is not a consideration.")
	case Contested:
		out = append(out, "Simulated bearer-risk policy penalises high-emission, infrastructure-dependent bearers.", "Prioritise path diversity and resilience.", "Mission policy may override network-performance ranking.")
	}
	switch st {
	case StateElevated:
		out = append(out, "State ELEVATED: reliability weighting increased.")
	case StateDegraded:
		out = append(out, "State DEGRADED: resilience and stability weighting increased; lower-value traffic throttled.")
	case StateCritical:
		out = append(out, "State CRITICAL: preservation of critical traffic maximised; non-essential bearer characteristics de-emphasised.")
	}
	return out
}

// Scenario is a one-click mission preset (spec §10): it sets a profile + state
// and carries a caption. The environment shaping (which bearers degrade) is
// applied by the caller that owns the simulator, keyed by ID.
type Scenario struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Profile Profile `json:"profile"`
	State   State   `json:"state"`
	Caption string  `json:"caption"`
}

// Scenarios returns the built-in mission scenario presets. All are simulated.
func Scenarios() []Scenario {
	return []Scenario{
		{"routine-mobility", "Routine Mobility", Routine, StateNormal,
			"Healthy 5G, SATCOM held in reserve, Wi-Fi intermittent — routine mobility. 5G is preferred."},
		{"coverage-loss", "Coverage Loss", Routine, StateElevated,
			"5G coverage degrades while SATCOM stays available. Continuity switches once the policy threshold is crossed."},
		{"mission-priority-override", "Mission Priority Override", MissionCritical, StateCritical,
			"Network conditions unchanged and 5G still measures best — but the mission is now critical, so mission policy selects the more resilient path."},
		{"critical-traffic-event", "Critical Traffic Event", Emergency, StateCritical,
			"Command traffic activated under an emergency state: critical traffic is prioritised while bulk and background traffic are deferred or suspended."},
		{"contested", "Contested / Degraded Environment", Contested, StateDegraded,
			"Multiple bearers degrade differently under a simulated contested environment; mission policy re-weights bearer preference toward diversity and resilience."},
	}
}

// ScenarioByID returns a scenario preset by id.
func ScenarioByID(id string) (Scenario, bool) {
	for _, s := range Scenarios() {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}
