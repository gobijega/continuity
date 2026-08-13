// Package agent is the Continuity control loop. Each tick it samples every
// bearer, scores it, asks the policy controller for a decision, applies that
// decision through the route manager, records events, and publishes an
// immutable snapshot for the API/dashboard. It runs over a pluggable Source, so
// the same loop drives a live node or a simulated demo.
package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gobijega/continuity/internal/events"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/routing"
	"github.com/gobijega/continuity/internal/scoring"
	"github.com/gobijega/continuity/internal/telemetry"
	"github.com/gobijega/continuity/internal/tunnel"
)

// Reading is one bearer's raw sample for a cycle.
type Reading struct {
	Name      string
	Kind      string
	Preferred bool
	Metrics   telemetry.Metrics
}

// Source yields the current bearer readings each tick (live or simulated).
type Source interface {
	Read(now time.Time) []Reading
}

// BearerView is the per-bearer snapshot published to the API.
type BearerView struct {
	Name       string             `json:"name"`
	Kind       string             `json:"kind"`
	Metrics    telemetry.Metrics  `json:"metrics"`
	Score      float64            `json:"score"`
	Components map[string]float64 `json:"components,omitempty"`
	Role       string             `json:"role"`
	Preferred  bool               `json:"preferred"`
}

// Snapshot is an immutable view of the agent's state for the API/dashboard.
type Snapshot struct {
	Node       string         `json:"node"`
	Profile    string         `json:"profile"`
	Status     string         `json:"status"` // RESILIENT | DEGRADED | CRITICAL
	Active     string         `json:"active_interface"`
	At         time.Time      `json:"at"`
	Bearers    []BearerView   `json:"bearers"`
	Events     []events.Event `json:"events"`
	Continuity *tunnel.State  `json:"continuity,omitempty"` // session-continuity overlay (Sprint 9)
}

// Agent runs the control loop and holds the latest snapshot.
type Agent struct {
	node       string
	profile    string
	weights    scoring.Weights
	thresh     scoring.Thresholds
	degradedAt float64

	source Source
	ctrl   *policy.Controller
	router routing.Manager
	tunnel tunnel.Manager
	log    *events.Log

	prevState map[string]string
	mu        sync.RWMutex
	snap      Snapshot
}

// Options configures a new Agent.
type Options struct {
	Node    string
	Profile string
	Source  Source
	Router  routing.Manager
	Tunnel  tunnel.Manager
	Hyst    policy.Hysteresis
	Thresh  scoring.Thresholds
	Log     *events.Log
}

// New builds an Agent from Options, filling in sensible defaults.
func New(o Options) *Agent {
	if o.Router == nil {
		o.Router = routing.NewDryRun()
	}
	if o.Tunnel == nil {
		o.Tunnel = tunnel.NewDryRun(tunnel.DefaultOverlay, 3)
	}
	if o.Log == nil {
		o.Log = events.NewLog(200)
	}
	if o.Profile == "" {
		o.Profile = "default"
	}
	if o.Node == "" {
		o.Node = "edge-node"
	}
	if (o.Thresh == scoring.Thresholds{}) {
		o.Thresh = scoring.DefaultThresholds()
	}
	if (o.Hyst == policy.Hysteresis{}) {
		o.Hyst = policy.DefaultHysteresis()
	}
	return &Agent{
		node:       o.Node,
		profile:    o.Profile,
		weights:    scoring.ProfileWeights(o.Profile),
		thresh:     o.Thresh,
		degradedAt: o.Hyst.RecoveryThreshold,
		source:     o.Source,
		ctrl:       policy.NewController(o.Hyst),
		router:     o.Router,
		tunnel:     o.Tunnel,
		log:        o.Log,
		prevState:  map[string]string{},
	}
}

// Tick runs one control cycle at time now and returns the fresh snapshot.
func (a *Agent) Tick(now time.Time) Snapshot {
	readings := a.source.Read(now)

	views := make([]BearerView, 0, len(readings))
	scored := make([]policy.Scored, 0, len(readings))
	for _, r := range readings {
		in := scoring.Input{
			Available:      r.Metrics.OK,
			LatencyMs:      r.Metrics.LatencyMs,
			JitterMs:       r.Metrics.JitterMs,
			LossPct:        r.Metrics.LossPct,
			ThroughputMbps: r.Metrics.ThroughputMbps,
			ReliabilityPct: 100,
			CostScore:      costScore(r.Kind),
		}
		sc := scoring.Compute(in, a.weights, a.thresh)
		v := BearerView{Name: r.Name, Kind: r.Kind, Metrics: r.Metrics, Score: sc.Total, Components: sc.Components, Preferred: r.Preferred}
		a.emitTransition(now, v)
		views = append(views, v)
		scored = append(scored, policy.Scored{Name: r.Name, Score: sc.Total, Available: r.Metrics.OK, Preferred: r.Preferred})
	}

	d := a.ctrl.Decide(now, scored)
	if d.Action == policy.Migrate {
		_ = a.router.Activate(d.To)
		a.log.Add(events.Event{At: now, Node: a.node, Interface: d.To, Type: "MIGRATE", From: d.From, To: d.To, Reason: d.Reason})
		// Move the encrypted overlay onto the new bearer. The security
		// association is untouched, so application sessions ride through the
		// failover; d.From == "" is the initial bind, which preserves nothing.
		if err := a.tunnel.Rebind(d.To); err == nil && d.From != "" {
			if ts := a.tunnel.State(); ts.Enabled {
				a.log.Add(events.Event{At: now, Node: a.node, Interface: d.To, Type: "REBIND", From: d.From, To: d.To,
					Reason: fmt.Sprintf("session continuity: %d session(s) preserved on %s", ts.Sessions, ts.Overlay)})
			}
		}
	}
	active := a.ctrl.Active()

	sort.SliceStable(views, func(i, j int) bool { return views[i].Score > views[j].Score })
	availCount := 0
	for i := range views {
		if views[i].Metrics.OK {
			availCount++
		}
		switch {
		case views[i].Name == active:
			views[i].Role = "ACTIVE"
		case !views[i].Metrics.OK:
			views[i].Role = "DOWN"
		default:
			views[i].Role = "STANDBY"
		}
	}

	snap := Snapshot{
		Node:       a.node,
		Profile:    a.profile,
		Status:     status(active, availCount),
		Active:     active,
		At:         now,
		Bearers:    views,
		Events:     a.log.Recent(12),
		Continuity: continuityView(a.tunnel),
	}
	a.mu.Lock()
	a.snap = snap
	a.mu.Unlock()
	return snap
}

// emitTransition logs an event when a bearer changes health class.
func (a *Agent) emitTransition(now time.Time, v BearerView) {
	cur := "ok"
	if !v.Metrics.OK {
		cur = "down"
	} else if v.Score < a.degradedAt {
		cur = "degraded"
	}
	prev := a.prevState[v.Name]
	a.prevState[v.Name] = cur
	if prev == "" || prev == cur {
		return
	}
	switch cur {
	case "down":
		a.log.Add(events.Event{At: now, Node: a.node, Interface: v.Name, Type: "LINK_DOWN", Reason: "interface lost"})
	case "degraded":
		a.log.Add(events.Event{At: now, Node: a.node, Interface: v.Name, Type: "DEGRADED",
			Reason: fmt.Sprintf("loss %.0f%%, latency %.0fms", v.Metrics.LossPct, v.Metrics.LatencyMs)})
	case "ok":
		a.log.Add(events.Event{At: now, Node: a.node, Interface: v.Name, Type: "HEALTHY", Reason: "link recovered"})
	}
}

// Run drives the loop until ctx is cancelled.
func (a *Agent) Run(ctx context.Context, interval time.Duration) {
	a.Tick(time.Now())
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tk.C:
			a.Tick(t)
		}
	}
}

// Snapshot returns the most recent published state.
func (a *Agent) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snap
}

// Continuity returns the current session-continuity (overlay tunnel) state.
func (a *Agent) Continuity() tunnel.State { return a.tunnel.State() }

// continuityView returns the manager's state as a pointer, or nil when session
// continuity is disabled so the JSON omits it entirely.
func continuityView(m tunnel.Manager) *tunnel.State {
	st := m.State()
	if !st.Enabled {
		return nil
	}
	return &st
}

func status(active string, availCount int) string {
	if active == "" || availCount == 0 {
		return "CRITICAL"
	}
	if availCount >= 2 {
		return "RESILIENT"
	}
	return "DEGRADED"
}

func costScore(kind string) float64 {
	if kind == "cellular" {
		return 40
	}
	return 100
}
