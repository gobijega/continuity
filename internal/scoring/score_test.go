package scoring

import "testing"

func TestBandBoundaries(t *testing.T) {
	if got := band(10, 20, 600); got != 100 {
		t.Errorf("band below good = %v, want 100", got)
	}
	if got := band(600, 20, 600); got != 0 {
		t.Errorf("band at/above bad = %v, want 0", got)
	}
	mid := band(310, 20, 600) // midpoint
	if mid < 49 || mid > 51 {
		t.Errorf("band midpoint = %v, want ~50", mid)
	}
}

func TestComputeExcellentVsPoor(t *testing.T) {
	w := ProfileWeights("default")
	th := DefaultThresholds()

	excellent := Compute(Input{
		Available: true, LatencyMs: 15, JitterMs: 2, LossPct: 0,
		ThroughputMbps: 100, ReliabilityPct: 100, CostScore: 100,
	}, w, th)
	if excellent.Total < 85 {
		t.Errorf("excellent link scored %v, want >= 85", excellent.Total)
	}

	poor := Compute(Input{
		Available: true, LatencyMs: 500, JitterMs: 60, LossPct: 10,
		ThroughputMbps: 1, ReliabilityPct: 50, CostScore: 100,
	}, w, th)
	if poor.Total > 45 {
		t.Errorf("poor link scored %v, want <= 45", poor.Total)
	}
	if poor.Total >= excellent.Total {
		t.Errorf("poor (%v) should score below excellent (%v)", poor.Total, excellent.Total)
	}
}

func TestUnavailableScoresZero(t *testing.T) {
	s := Compute(Input{Available: false, LatencyMs: 5}, ProfileWeights("voice"), DefaultThresholds())
	if s.Total != 0 {
		t.Errorf("unavailable bearer scored %v, want 0", s.Total)
	}
}

// The point of profile weights: the same two links rank differently depending
// on the application. A low-latency/low-bandwidth link should win for voice;
// a high-bandwidth/high-latency link should win for bulk transfer.
func TestProfileChangesRanking(t *testing.T) {
	th := DefaultThresholds()
	lowLatency := Input{Available: true, LatencyMs: 20, JitterMs: 3, LossPct: 0, ThroughputMbps: 3, ReliabilityPct: 100, CostScore: 100}
	highBandwidth := Input{Available: true, LatencyMs: 320, JitterMs: 20, LossPct: 1, ThroughputMbps: 200, ReliabilityPct: 100, CostScore: 100}

	voiceA := Compute(lowLatency, ProfileWeights("voice"), th).Total
	voiceB := Compute(highBandwidth, ProfileWeights("voice"), th).Total
	if voiceA <= voiceB {
		t.Errorf("under voice, low-latency (%v) should beat high-bandwidth (%v)", voiceA, voiceB)
	}

	bulkA := Compute(lowLatency, ProfileWeights("bulk"), th).Total
	bulkB := Compute(highBandwidth, ProfileWeights("bulk"), th).Total
	if bulkB <= bulkA {
		t.Errorf("under bulk, high-bandwidth (%v) should beat low-latency (%v)", bulkB, bulkA)
	}
}

func TestNormalizedSumsToOne(t *testing.T) {
	n := ProfileWeights("video").Normalized()
	if s := n.Sum(); s < 0.999 || s > 1.001 {
		t.Errorf("normalized weights sum = %v, want 1", s)
	}
}
