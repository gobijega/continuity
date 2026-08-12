package simulator

import "fmt"

// NetemAddArgs builds the `tc` arguments that impair a real interface with
// added delay, jitter and loss — for a physical test bench (spec §20).
// Requires CAP_NET_ADMIN. Verifiable without executing (see tests).
func NetemAddArgs(iface string, delayMs, jitterMs int, lossPct float64) []string {
	return []string{
		"qdisc", "replace", "dev", iface, "root", "netem",
		"delay", fmt.Sprintf("%dms", delayMs), fmt.Sprintf("%dms", jitterMs),
		"loss", fmt.Sprintf("%.1f%%", lossPct),
	}
}

// NetemClearArgs builds the `tc` arguments that remove impairment from iface.
func NetemClearArgs(iface string) []string {
	return []string{"qdisc", "del", "dev", iface, "root", "netem"}
}
