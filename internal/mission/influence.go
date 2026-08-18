package mission

import "github.com/gobijega/continuity/internal/scoring"

// Influence quantifies how strongly mission context shaped the current decision
// (spec §9). It is a mathematically-derived, deterministic metric — not a vague
// label — built from two measurable quantities:
//
//   - weightDivergence: the total-variation distance between the mission-aware
//     weight vector and the neutral network-only weight vector (both
//     normalised). 0 means "mission is weighing bearers exactly like a
//     network-only selector"; 1 means "completely different priorities".
//   - suitabilityDeviation: how far the mission's per-bearer suitability
//     multipliers sit from neutral (1.0), i.e. how hard mission policy is
//     preferring or penalising bearer types.
//
// The two combine into a 0..100 percentage. When mission policy actually
// changed the selected bearer (override), the percentage is floored into the
// HIGH band, because a changed decision is by definition a strong influence.
func Influence(missionW, networkW scoring.Weights, suit map[string]float64, override bool) (pct float64, band string) {
	tv := totalVariation(missionW.Normalized(), networkW.Normalized()) // [0,1]

	var dev, n float64
	for _, s := range suit {
		d := s - 1
		if d < 0 {
			d = -d
		}
		dev += d
		n++
	}
	if n > 0 {
		dev /= n
	}
	sd := dev / 0.25 // 25% average deviation reads as full-scale
	sd = clampF(sd, 0, 1)

	raw := 100 * clampF(0.7*tv+0.3*sd, 0, 1)
	if override {
		if raw < 72 {
			raw = 72
		}
	}
	return roundTo1(raw), bandFor(raw)
}

// totalVariation is 0.5 * sum of absolute per-component differences between two
// normalised weight vectors — the standard total-variation distance, in [0,1].
func totalVariation(a, b scoring.Weights) float64 {
	sum := absF(a.Latency-b.Latency) +
		absF(a.Loss-b.Loss) +
		absF(a.Jitter-b.Jitter) +
		absF(a.Bandwidth-b.Bandwidth) +
		absF(a.Reliability-b.Reliability) +
		absF(a.Availability-b.Availability) +
		absF(a.Cost-b.Cost) +
		absF(a.Resilience-b.Resilience)
	return clampF(0.5*sum, 0, 1)
}

func bandFor(pct float64) string {
	switch {
	case pct >= 80:
		return "DOMINANT"
	case pct >= 55:
		return "HIGH"
	case pct >= 25:
		return "MODERATE"
	default:
		return "LOW"
	}
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func roundTo1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
