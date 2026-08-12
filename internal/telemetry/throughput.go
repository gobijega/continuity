package telemetry

import (
	"os"
	"strconv"
	"strings"
)

// LinkSpeedMbps reports the negotiated link speed for an interface from Linux
// sysfs, in Mbit/s. It is a capacity ceiling, not a live throughput
// measurement — passive/active throughput estimation is planned for a later
// sprint (spec §7). Returns (speed, true) on success, (0, false) otherwise.
//
// Many virtual and wireless devices report -1 (unknown); those return false.
func LinkSpeedMbps(iface string) (float64, bool) {
	b, err := os.ReadFile("/sys/class/net/" + iface + "/speed")
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || v <= 0 {
		return 0, false
	}
	return float64(v), true
}
