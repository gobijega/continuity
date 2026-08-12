// Package scoring turns a bearer's raw health metrics into a single normalised
// 0–100 score (spec §8, Sprint 3). The mapping is deliberately explicit and
// weight-driven rather than a black box: different application profiles weight
// the components differently (voice cares about latency and jitter; bulk
// transfer cares about bandwidth and cost), and every sub-score is reported so
// a decision can always be explained.
package scoring

import "strings"

// Weights sets the relative importance of each health component. They need not
// sum to 1; Compute normalises them.
type Weights struct {
	Latency      float64 `json:"latency"`
	Loss         float64 `json:"loss"`
	Jitter       float64 `json:"jitter"`
	Bandwidth    float64 `json:"bandwidth"`
	Reliability  float64 `json:"reliability"`
	Availability float64 `json:"availability"`
	Cost         float64 `json:"cost"`
}

// Sum returns the total of all weights.
func (w Weights) Sum() float64 {
	return w.Latency + w.Loss + w.Jitter + w.Bandwidth + w.Reliability + w.Availability + w.Cost
}

// Normalized returns weights scaled so they sum to 1. A zero-sum set is
// returned unchanged.
func (w Weights) Normalized() Weights {
	s := w.Sum()
	if s == 0 {
		return w
	}
	return Weights{
		Latency:      w.Latency / s,
		Loss:         w.Loss / s,
		Jitter:       w.Jitter / s,
		Bandwidth:    w.Bandwidth / s,
		Reliability:  w.Reliability / s,
		Availability: w.Availability / s,
		Cost:         w.Cost / s,
	}
}

// ProfileWeights returns the weight set for a named application profile
// (spec §8). Unknown names fall back to a balanced default.
func ProfileWeights(profile string) Weights {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "voice":
		return Weights{Latency: 0.28, Jitter: 0.24, Loss: 0.24, Availability: 0.10, Reliability: 0.10, Bandwidth: 0.04}
	case "video":
		return Weights{Bandwidth: 0.30, Loss: 0.25, Latency: 0.20, Jitter: 0.10, Availability: 0.08, Reliability: 0.07}
	case "telemetry":
		return Weights{Reliability: 0.30, Availability: 0.30, Loss: 0.20, Latency: 0.10, Jitter: 0.05, Bandwidth: 0.05}
	case "bulk":
		return Weights{Bandwidth: 0.45, Cost: 0.25, Loss: 0.15, Reliability: 0.10, Availability: 0.05}
	default:
		return Weights{Latency: 0.18, Loss: 0.18, Jitter: 0.12, Bandwidth: 0.15, Reliability: 0.15, Availability: 0.15, Cost: 0.07}
	}
}

// Profiles lists the built-in profile names.
func Profiles() []string {
	return []string{"default", "voice", "video", "telemetry", "bulk"}
}
