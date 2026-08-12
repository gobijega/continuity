package agent

import (
	"time"

	"github.com/gobijega/continuity/internal/simulator"
)

// SimSource reads bearer metrics from a simulator, so the whole control loop
// runs without root or real radios. It exposes the underlying Sim so the API
// can trigger degrade/restore during a demo.
type SimSource struct {
	sim       *simulator.Sim
	preferred string
}

// NewSimSource wraps a simulator, marking preferred as the home bearer.
func NewSimSource(sim *simulator.Sim, preferred string) *SimSource {
	return &SimSource{sim: sim, preferred: preferred}
}

// Read returns a Reading per simulated bearer.
func (s *SimSource) Read(now time.Time) []Reading {
	names := s.sim.Names()
	out := make([]Reading, 0, len(names))
	for _, n := range names {
		out = append(out, Reading{
			Name:      n,
			Kind:      s.sim.Kind(n),
			Preferred: n == s.preferred,
			Metrics:   s.sim.Sample(n, now),
		})
	}
	return out
}

// Sim returns the underlying simulator (for the API's degrade/restore).
func (s *SimSource) Sim() *simulator.Sim { return s.sim }
