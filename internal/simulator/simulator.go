// Package simulator produces synthetic bearer telemetry so the full Continuity
// pipeline — scoring, policy, failover, dashboard — can run without root or
// real radios (spec §20). It also builds tc/netem commands for degrading real
// interfaces on a physical test bench (see netem.go).
package simulator

import (
	"sync"
	"time"

	"github.com/gobijega/continuity/internal/telemetry"
)

// Bearer is one simulated link: a healthy baseline plus any applied impairment.
type Bearer struct {
	Name string
	Kind string

	baseLatency  float64
	baseJitter   float64
	baseLoss     float64
	baseTputMbps float64

	addLatency float64
	addJitter  float64
	loss       float64
	tputFactor float64
	down       bool
}

// Sim is a set of simulated bearers, safe for concurrent use.
type Sim struct {
	mu      sync.Mutex
	order   []string
	bearers map[string]*Bearer
}

// NewDemo returns the classic three-bearer demo set: a strong preferred 5G
// link, a solid SATCOM backup, and a mediocre (lossy) Wi-Fi link — so a
// degraded 5G fails over to SATCOM, then recovers.
func NewDemo() *Sim {
	s := &Sim{bearers: map[string]*Bearer{}}
	s.add(&Bearer{Name: "5g", Kind: "cellular", baseLatency: 28, baseJitter: 4, baseLoss: 0, baseTputMbps: 120})
	s.add(&Bearer{Name: "satcom", Kind: "tunnel", baseLatency: 140, baseJitter: 18, baseLoss: 0, baseTputMbps: 45})
	s.add(&Bearer{Name: "wifi", Kind: "wifi", baseLatency: 40, baseJitter: 10, baseLoss: 8, baseTputMbps: 70})
	return s
}

func (s *Sim) add(b *Bearer) {
	b.tputFactor = 1
	s.bearers[b.Name] = b
	s.order = append(s.order, b.Name)
}

// Names returns the bearer names in a stable order.
func (s *Sim) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// Kind returns a bearer's kind, or "" if unknown.
func (s *Sim) Kind(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bearers[name]; b != nil {
		return b.Kind
	}
	return ""
}

// Sample returns the current metrics for a bearer, folding in any impairment.
func (s *Sim) Sample(name string, now time.Time) telemetry.Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bearers[name]
	if b == nil {
		return telemetry.Metrics{Interface: name, At: now}
	}
	if b.down {
		return telemetry.Metrics{Interface: name, Target: "sim", Samples: 4, Success: 0, LossPct: 100, OK: false, At: now}
	}
	loss := b.baseLoss + b.loss
	if loss > 100 {
		loss = 100
	}
	return telemetry.Metrics{
		Interface:      name,
		Target:         "sim",
		Samples:        4,
		Success:        4,
		LatencyMs:      b.baseLatency + b.addLatency,
		JitterMs:       b.baseJitter + b.addJitter,
		LossPct:        loss,
		ThroughputMbps: b.baseTputMbps * b.tputFactor,
		OK:             true,
		At:             now,
	}
}

// Degrade applies an impairment to a bearer (added latency/jitter, absolute
// extra loss, and a throughput multiplier).
func (s *Sim) Degrade(name string, addLatencyMs, addJitterMs, lossPct, tputFactor float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bearers[name]; b != nil {
		if tputFactor <= 0 {
			tputFactor = 0.1
		}
		b.addLatency, b.addJitter, b.loss, b.tputFactor, b.down = addLatencyMs, addJitterMs, lossPct, tputFactor, false
	}
}

// Outage takes a bearer fully down.
func (s *Sim) Outage(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bearers[name]; b != nil {
		b.down = true
	}
}

// Restore clears all impairment from a bearer (returning it to baseline).
func (s *Sim) Restore(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bearers[name]; b != nil {
		b.addLatency, b.addJitter, b.loss, b.tputFactor, b.down = 0, 0, 0, 1, false
	}
}

// Scenarios returns the built-in preset impairments (spec §20).
func Scenarios() map[string]func(*Sim) {
	return map[string]func(*Sim){
		"CELLULAR_CONGESTION": func(s *Sim) { s.Degrade("5g", 380, 40, 20, 0.2) },
		"SATELLITE_OUTAGE":    func(s *Sim) { s.Outage("satcom") },
		"HIGH_PACKET_LOSS":    func(s *Sim) { s.Degrade("wifi", 60, 30, 25, 0.5) },
		"LINK_RECOVERY":       func(s *Sim) { s.Restore("5g"); s.Restore("satcom"); s.Restore("wifi") },
	}
}
