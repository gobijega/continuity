package mission

import "github.com/gobijega/continuity/internal/scoring"

// This file turns a mission profile + state + dominant traffic class into the
// concrete network-component weighting the scoring engine uses (spec §6). It is
// the "mission-derived performance weighting" the Mission Context Engine
// exposes. Every weight set is normalised by the scoring layer before use, so
// the numbers below are relative, not absolute.

// profileWeights is each mission profile's base network-component weighting,
// following the suggested weightings in spec §3. Resilience is the structural
// dimension (see scoring.Weights): routine ops give it modest weight; assured,
// emergency and contested ops weight it heavily. A conventional network-only
// selector would leave it at zero (see NetworkOnlyWeights).
var profileWeights = map[Profile]scoring.Weights{
	// Maximise useful throughput, favour economical high-capacity connectivity.
	Routine: {Bandwidth: 0.24, Latency: 0.18, Cost: 0.16, Reliability: 0.10, Loss: 0.10, Jitter: 0.06, Availability: 0.06, Resilience: 0.10},
	// Preserve continuity; reliability and resilience over raw throughput.
	MissionCritical: {Resilience: 0.22, Reliability: 0.20, Loss: 0.18, Availability: 0.12, Latency: 0.10, Jitter: 0.08, Bandwidth: 0.06, Cost: 0.04},
	// Maximise the probability that critical messages remain deliverable.
	Emergency: {Resilience: 0.26, Availability: 0.22, Loss: 0.16, Reliability: 0.14, Latency: 0.10, Jitter: 0.06, Bandwidth: 0.04, Cost: 0.02},
	// Maintain comms under simulated degraded conditions; diversity + resilience.
	// (The contested "bearer-risk factor" is applied via bearer suitability, not
	// a scoring component — see Suitability.)
	Contested: {Resilience: 0.26, Reliability: 0.20, Availability: 0.16, Loss: 0.16, Latency: 0.08, Jitter: 0.06, Bandwidth: 0.06, Cost: 0.02},
}

// ProfileWeights returns a mission profile's base network weighting.
func ProfileWeights(p Profile) scoring.Weights {
	if w, ok := profileWeights[p]; ok {
		return w
	}
	return profileWeights[Routine]
}

// stateEmphasis is a per-component multiplier applied on top of the profile
// weights so the operational state visibly sharpens the weighting even within a
// single profile (spec §4). NORMAL is identity; higher states progressively
// amplify reliability / availability / resilience / loss and damp bandwidth,
// cost and latency. This is the mechanism by which the SAME network conditions
// can yield a DIFFERENT decision once the mission state changes.
func stateEmphasis(st State) scoring.Weights {
	switch st {
	case StateElevated:
		return scoring.Weights{Latency: 1.0, Loss: 1.25, Jitter: 1.0, Bandwidth: 0.9, Reliability: 1.3, Availability: 1.15, Cost: 0.95, Resilience: 1.2}
	case StateDegraded:
		return scoring.Weights{Latency: 0.9, Loss: 1.3, Jitter: 1.0, Bandwidth: 0.7, Reliability: 1.5, Availability: 1.4, Cost: 0.7, Resilience: 1.5}
	case StateCritical:
		return scoring.Weights{Latency: 0.6, Loss: 1.4, Jitter: 0.8, Bandwidth: 0.4, Reliability: 1.7, Availability: 1.8, Cost: 0.3, Resilience: 1.9}
	default: // StateNormal
		return scoring.Weights{Latency: 1.0, Loss: 1.0, Jitter: 1.0, Bandwidth: 1.0, Reliability: 1.0, Availability: 1.0, Cost: 1.0, Resilience: 1.0}
	}
}

// classShare is how much of the final weighting the dominant traffic class's
// application profile contributes; the mission profile+state weighting supplies
// the rest. Kept modest so mission policy leads and the active application
// shades it.
const classShare = 0.25

// MissionWeights composes the final network weighting from the mission profile,
// operational state and the dominant traffic class:
//
//	profile weights  ×  state emphasis        → mission emphasis (normalised)
//	blended with the dominant class's app profile (classShare)
//
// The result is normalised. This is the weight vector fed to the scoring engine
// for the mission-aware score.
func MissionWeights(p Profile, st State, dominant TrafficClass) scoring.Weights {
	emphasised := mulWeights(ProfileWeights(p), stateEmphasis(st)).Normalized()
	classW := scoring.ProfileWeights(dominant.Profile()).Normalized()
	return blendWeights(emphasised, classW, 1-classShare).Normalized()
}

// NetworkOnlyWeights is the conventional, connectivity-first weighting that
// stands in for "what a network-metric-only selector would choose": latency,
// bandwidth and loss lead, with no structural-resilience term. It is the
// baseline the mission-aware decision is compared against (spec §8) and the
// reference the mission-influence metric measures divergence from.
func NetworkOnlyWeights() scoring.Weights {
	return scoring.Weights{Latency: 0.26, Bandwidth: 0.24, Loss: 0.16, Jitter: 0.12, Availability: 0.08, Reliability: 0.08, Cost: 0.06}
}

// Thresholds is the metric→sub-score mapping used throughout the mission-aware
// path. It raises the bandwidth reference so a high-capacity bearer's throughput
// genuinely differentiates it (a 120 Mbps 5G link vs a 45 Mbps SATCOM link),
// which is what lets routine ops clearly prefer 5G while assured ops prefer the
// more resilient path.
func Thresholds() scoring.Thresholds {
	t := scoring.DefaultThresholds()
	t.BandwidthRefMbps = 100
	return t
}

// mulWeights multiplies two weight sets component-wise.
func mulWeights(a, b scoring.Weights) scoring.Weights {
	return scoring.Weights{
		Latency:      a.Latency * b.Latency,
		Loss:         a.Loss * b.Loss,
		Jitter:       a.Jitter * b.Jitter,
		Bandwidth:    a.Bandwidth * b.Bandwidth,
		Reliability:  a.Reliability * b.Reliability,
		Availability: a.Availability * b.Availability,
		Cost:         a.Cost * b.Cost,
		Resilience:   a.Resilience * b.Resilience,
	}
}

// blendWeights returns aShare*a + (1-aShare)*b, component-wise.
func blendWeights(a, b scoring.Weights, aShare float64) scoring.Weights {
	bShare := 1 - aShare
	return scoring.Weights{
		Latency:      aShare*a.Latency + bShare*b.Latency,
		Loss:         aShare*a.Loss + bShare*b.Loss,
		Jitter:       aShare*a.Jitter + bShare*b.Jitter,
		Bandwidth:    aShare*a.Bandwidth + bShare*b.Bandwidth,
		Reliability:  aShare*a.Reliability + bShare*b.Reliability,
		Availability: aShare*a.Availability + bShare*b.Availability,
		Cost:         aShare*a.Cost + bShare*b.Cost,
		Resilience:   aShare*a.Resilience + bShare*b.Resilience,
	}
}
