package scoring

import "math"

// Thresholds define how each raw metric maps onto a 0–100 sub-score. Keeping
// them explicit (rather than buried in constants) is what makes a score
// explainable and tunable by policy.
type Thresholds struct {
	LatencyGoodMs    float64 // <= scores 100
	LatencyBadMs     float64 // >= scores 0
	JitterGoodMs     float64
	JitterBadMs      float64
	LossBadPct       float64 // 0% => 100, >= this => 0
	BandwidthRefMbps float64 // >= this => 100
}

// DefaultThresholds returns reasonable engineering defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{
		LatencyGoodMs:    20,
		LatencyBadMs:     600,
		JitterGoodMs:     5,
		JitterBadMs:      80,
		LossBadPct:       12,
		BandwidthRefMbps: 25,
	}
}

// Input is the per-bearer evidence the score is computed from. Fields left at
// their zero value are treated as "unknown" where noted.
type Input struct {
	Available      bool
	LatencyMs      float64
	JitterMs       float64
	LossPct        float64
	ThroughputMbps float64 // <= 0 means unknown (scored neutrally)
	ReliabilityPct float64 // 0..100 historical success; use 100 if unknown
	CostScore      float64 // 0..100 (100 = free/unmetered); use 100 if unknown
}

// Score is a total in [0,100] plus the component sub-scores that produced it.
type Score struct {
	Total      float64            `json:"total"`
	Components map[string]float64 `json:"components"`
}

// Compute maps in onto a 0–100 score under the given weights and thresholds.
// An unavailable bearer scores 0 (unusable), matching the spec's 0..100 scale.
func Compute(in Input, w Weights, t Thresholds) Score {
	if !in.Available {
		return Score{Total: 0, Components: map[string]float64{"availability": 0}}
	}

	comp := map[string]float64{
		"latency":      band(in.LatencyMs, t.LatencyGoodMs, t.LatencyBadMs),
		"jitter":       band(in.JitterMs, t.JitterGoodMs, t.JitterBadMs),
		"loss":         band(in.LossPct, 0, t.LossBadPct),
		"reliability":  clamp(in.ReliabilityPct, 0, 100),
		"availability": 100,
		"cost":         clamp(in.CostScore, 0, 100),
	}
	if in.ThroughputMbps <= 0 {
		comp["bandwidth"] = 50 // neutral when capacity is unknown
	} else {
		comp["bandwidth"] = clamp(100*in.ThroughputMbps/t.BandwidthRefMbps, 0, 100)
	}

	wn := w.Normalized()
	total := comp["latency"]*wn.Latency +
		comp["loss"]*wn.Loss +
		comp["jitter"]*wn.Jitter +
		comp["bandwidth"]*wn.Bandwidth +
		comp["reliability"]*wn.Reliability +
		comp["availability"]*wn.Availability +
		comp["cost"]*wn.Cost

	return Score{Total: round1(clamp(total, 0, 100)), Components: comp}
}

// band maps x onto [0,100]: 100 at x<=good, 0 at x>=bad, linear between.
// Lower x is always better (latency, jitter, loss).
func band(x, good, bad float64) float64 {
	if bad <= good {
		if x <= good {
			return 100
		}
		return 0
	}
	if x <= good {
		return 100
	}
	if x >= bad {
		return 0
	}
	return 100 * (bad - x) / (bad - good)
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
