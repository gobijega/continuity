// Package telemetry measures per-bearer link health — latency, jitter, packet
// loss and throughput (spec §6, Sprint 2). Probes are source-bound so each
// measurement egresses a specific interface, giving independent health per
// bearer even when several are up at once.
package telemetry

import "math"

// Mean returns the arithmetic mean of xs, or 0 for an empty slice.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// StdDev returns the population standard deviation of xs, or 0 for fewer than
// two samples.
func StdDev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := Mean(xs)
	var sq float64
	for _, x := range xs {
		d := x - m
		sq += d * d
	}
	return math.Sqrt(sq / float64(len(xs)))
}
