package interfaces

import (
	"os"
	"strings"
)

const sysNet = "/sys/class/net/"

// operState returns the Linux sysfs operational state for an interface
// (e.g. "up", "down", "unknown"), or "" if unavailable (non-Linux or error).
func operState(name string) string {
	b, err := os.ReadFile(sysNet + name + "/operstate")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// devType returns the DEVTYPE reported by the kernel in the interface's sysfs
// uevent file (e.g. "wlan", "wwan", "bridge", "vlan"), or "" if not present.
func devType(name string) string {
	b, err := os.ReadFile(sysNet + name + "/uevent")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "DEVTYPE="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// hasWireless reports whether the interface exposes a wireless sysfs node,
// which is a reliable signal that it is a Wi-Fi device.
func hasWireless(name string) bool {
	if _, err := os.Stat(sysNet + name + "/wireless"); err == nil {
		return true
	}
	return false
}

// classifyWithSys prefers authoritative sysfs facts (device type, wireless
// node) and falls back to name-based classification.
func classifyWithSys(name string) Kind {
	switch devType(name) {
	case "wlan":
		return KindWiFi
	case "wwan":
		return KindCellular
	case "bridge", "vlan", "bond", "team":
		return KindVirtual
	}
	if hasWireless(name) {
		return KindWiFi
	}
	return classify(name)
}
