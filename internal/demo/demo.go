// Package demo scripts the Continuity 0.1 showcase (Sprint 10): a repeatable,
// ~90-second timeline that drives the simulator through congestion, failover,
// recovery and fail-back while the agent reacts autonomously. It is the
// narrative layer over the simulator — deterministic and clock-injected, so the
// same script runs live behind the dashboard and under test. The runner never
// commands the agent; it only shapes the environment and narrates, so what the
// dashboard shows is the real control loop responding.
package demo

import (
	"sync"
	"time"

	"github.com/gobijega/continuity/internal/simulator"
)

// Step is one timed beat of the demonstration. Action may be nil for a beat
// that only narrates what the agent is expected to be doing.
type Step struct {
	At      time.Duration
	Caption string
	Action  func(s *simulator.Sim)
}

// tail is the dwell after the final beat before the script loops.
const tail = 10 * time.Second

// manualHold is how long a visitor-triggered scenario holds the simulator
// before the ambient demonstration auto-resumes, so a shared console returns to
// its looping showcase after someone stops interacting.
const manualHold = 60 * time.Second

// Script returns the built-in ~90-second demonstration: steady state, 5G
// congestion, failover to SATCOM with session continuity, recovery and
// fail-back — then it loops.
func Script() []Step {
	return []Step{
		{At: 0, Caption: "Steady state — resting on 5G; SATCOM and Wi-Fi held in reserve.",
			Action: func(s *simulator.Sim) { restoreAll(s) }},
		{At: 10 * time.Second, Caption: "Injecting 5G congestion — latency and packet loss climbing.",
			Action: func(s *simulator.Sim) { s.Degrade("5g", 380, 45, 22, 0.25) }},
		{At: 20 * time.Second, Caption: "5G degraded. Hysteresis holds briefly, then the agent fails over to SATCOM."},
		{At: 32 * time.Second, Caption: "Session continuity: the encrypted overlay rebinds to SATCOM — TCP sessions ride through the switch."},
		{At: 44 * time.Second, Caption: "Wi-Fi now taking packet loss too — SATCOM remains the strongest path.",
			Action: func(s *simulator.Sim) { s.Degrade("wifi", 70, 30, 26, 0.5) }},
		{At: 56 * time.Second, Caption: "5G link recovering…",
			Action: func(s *simulator.Sim) { s.Restore("5g") }},
		{At: 68 * time.Second, Caption: "Preferred path healthy — after the recovery hold, the agent fails back to 5G."},
		{At: 80 * time.Second, Caption: "Restored. RESILIENT throughout — zero sessions dropped.",
			Action: func(s *simulator.Sim) { s.Restore("wifi") }},
	}
}

// State is the runner's observable status, published to the dashboard/API.
type State struct {
	Running bool    `json:"running"`
	Paused  bool    `json:"paused"` // a manual scenario has taken over the simulator
	Caption string  `json:"caption"`
	Step    int     `json:"step"`  // beats applied so far (1-based within a cycle)
	Steps   int     `json:"steps"` // total beats in the script
	Elapsed float64 `json:"elapsed_s"`
	Total   float64 `json:"total_s"`
	Loops   int     `json:"loops"` // completed cycles
}

// Runner applies a Script against a Sim on an injected clock. Safe for
// concurrent use: Advance is called from the control loop, State from the API.
type Runner struct {
	sim    *simulator.Sim
	script []Step
	total  time.Duration

	mu       sync.Mutex
	start    time.Time
	idx      int
	caption  string
	elapsed  time.Duration
	loops    int
	running  bool
	paused   bool
	pausedAt time.Time
}

// New builds a Runner over the built-in Script.
func New(sim *simulator.Sim) *Runner { return NewWith(sim, Script()) }

// NewWith builds a Runner over a custom script (used by tests).
func NewWith(sim *simulator.Sim, script []Step) *Runner {
	total := tail
	if n := len(script); n > 0 {
		total = script[n-1].At + tail
	}
	return &Runner{sim: sim, script: script, total: total}
}

// Start begins the demonstration at now, applying the first beat immediately.
func (r *Runner) Start(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = true
	r.restart(now)
}

// Advance applies any beats now due and loops the script when it completes.
func (r *Runner) Advance(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	if r.paused {
		// A manual scenario owns the simulator; don't reshape it. Auto-resume
		// the ambient demonstration once the hold expires.
		if now.Sub(r.pausedAt) < manualHold {
			return
		}
		r.paused = false
		r.restart(now)
		r.elapsed = 0
		return
	}
	elapsed := now.Sub(r.start)
	for r.idx < len(r.script) && elapsed >= r.script[r.idx].At {
		r.applyStep(r.idx)
		r.idx++
	}
	if elapsed >= r.total {
		r.loops++
		r.restart(now)
		elapsed = 0
	}
	r.elapsed = elapsed
}

// Pause suspends the scripted demonstration so a manually triggered scenario
// can drive the simulator directly. The control loop keeps ticking, so the
// agent still reacts to whatever the manual scenario leaves in place.
func (r *Runner) Pause(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	r.paused = true
	r.pausedAt = now
}

// Resume clears the manual scenario and restarts the demonstration from steady
// state.
func (r *Runner) Resume(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	r.paused = false
	r.restart(now)
	r.elapsed = 0
}

// State returns the current runner status.
func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{
		Running: r.running,
		Paused:  r.paused,
		Caption: r.caption,
		Step:    r.idx,
		Steps:   len(r.script),
		Elapsed: r.elapsed.Seconds(),
		Total:   r.total.Seconds(),
		Loops:   r.loops,
	}
}

// restart returns the sim to baseline and re-applies the first beat. Caller
// holds the lock.
func (r *Runner) restart(now time.Time) {
	restoreAll(r.sim)
	r.start = now
	r.idx = 0
	r.elapsed = 0
	if len(r.script) > 0 && r.script[0].At == 0 {
		r.applyStep(0)
		r.idx = 1
	}
}

// applyStep runs a beat's action and sets its caption. Caller holds the lock.
func (r *Runner) applyStep(i int) {
	st := r.script[i]
	if st.Action != nil {
		st.Action(r.sim)
	}
	r.caption = st.Caption
}

func restoreAll(s *simulator.Sim) {
	for _, n := range s.Names() {
		s.Restore(n)
	}
}
