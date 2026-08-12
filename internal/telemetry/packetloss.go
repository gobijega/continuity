package telemetry

// LossPercent returns the percentage of probes that failed, in [0,100].
// A zero total yields 0 (no evidence of loss rather than a divide-by-zero).
func LossPercent(failed, total int) float64 {
	if total <= 0 {
		return 0
	}
	if failed < 0 {
		failed = 0
	}
	if failed > total {
		failed = total
	}
	return 100 * float64(failed) / float64(total)
}
