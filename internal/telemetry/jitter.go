package telemetry

import "math"

// Jitter estimates round-trip variation as the mean absolute difference
// between consecutive samples. This tracks short-term instability (the thing
// voice and interactive control care about) more faithfully than a plain
// standard deviation, which a slow drift can inflate. Returns 0 for fewer than
// two samples.
func Jitter(rttsMs []float64) float64 {
	if len(rttsMs) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(rttsMs); i++ {
		sum += math.Abs(rttsMs[i] - rttsMs[i-1])
	}
	return sum / float64(len(rttsMs)-1)
}
