// Package interfaces discovers and classifies the host's network interfaces.
//
// It is the foundation of the Continuity Edge Agent (spec §3.1, Sprint 1):
// before any bearer can be scored or steered, the agent must enumerate the
// interfaces available on the node and describe each one — name, type,
// operational state and addresses.
package interfaces

import (
	"net"
	"sort"
	"strings"
)

// Kind is a coarse classification of an interface by its likely bearer type.
type Kind string

const (
	KindEthernet Kind = "ethernet"
	KindWiFi     Kind = "wifi"
	KindCellular Kind = "cellular"
	KindTunnel   Kind = "tunnel" // VPN / WireGuard / tun / ppp
	KindVirtual  Kind = "virtual"
	KindLoopback Kind = "loopback"
	KindOther    Kind = "other"
)

// Interface describes a single discovered network interface.
type Interface struct {
	Name      string   `json:"name"`
	Index     int      `json:"index"`
	Kind      Kind     `json:"kind"`
	Up        bool     `json:"up"`         // admin up and carrier present
	OperState string   `json:"oper_state"` // sysfs operstate on Linux; "" otherwise
	MAC       string   `json:"mac,omitempty"`
	MTU       int      `json:"mtu"`
	IPv4      []string `json:"ipv4,omitempty"`
	IPv6      []string `json:"ipv6,omitempty"`
}

// PrimaryIPv4 returns the first global-unicast IPv4 address (private ranges
// included, link-local excluded), or "" if the interface has none. This is the
// address the telemetry layer binds probes to so traffic egresses this bearer.
func (i Interface) PrimaryIPv4() string {
	for _, a := range i.IPv4 {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil {
			continue
		}
		if ip4.IsGlobalUnicast() && !ip4.IsLinkLocalUnicast() {
			return ip4.String()
		}
	}
	return ""
}

// Usable reports whether the interface is a real, up bearer with an address —
// i.e. a candidate for scoring (not loopback, not virtual, not down).
func (i Interface) Usable() bool {
	if !i.Up || i.Kind == KindLoopback || i.Kind == KindVirtual {
		return false
	}
	return i.PrimaryIPv4() != ""
}

// Discover enumerates the host's network interfaces via the standard library
// and augments them, on Linux, with sysfs operational state and device type.
func Discover() ([]Interface, error) {
	sys, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(sys))
	for _, si := range sys {
		it := Interface{
			Name:  si.Name,
			Index: si.Index,
			MTU:   si.MTU,
			MAC:   si.HardwareAddr.String(),
			Up:    si.Flags&net.FlagUp != 0 && si.Flags&net.FlagRunning != 0,
		}
		if si.Flags&net.FlagLoopback != 0 {
			it.Kind = KindLoopback
		} else {
			it.Kind = classifyWithSys(si.Name)
		}
		it.OperState = operState(si.Name)

		addrs, _ := si.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if ip.To4() != nil {
				it.IPv4 = append(it.IPv4, ip.String())
			} else {
				it.IPv6 = append(it.IPv6, ip.String())
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Index < out[b].Index })
	return out, nil
}

// classify infers a Kind from the interface name using common Linux naming
// conventions. It is a heuristic fallback; classifyWithSys prefers sysfs facts.
func classify(name string) Kind {
	n := strings.ToLower(name)
	switch {
	case hasAnyPrefix(n, "ww", "wwan", "wwp", "rmnet", "qmi", "cdc-wdm"):
		return KindCellular
	case hasAnyPrefix(n, "wl", "wlan", "wlp", "wlo", "wlx"):
		return KindWiFi
	case hasAnyPrefix(n, "tun", "tap", "wg", "utun", "ppp", "ipsec", "gre", "sit", "tailscale", "zt"):
		return KindTunnel
	case hasAnyPrefix(n, "docker", "veth", "br", "virbr", "vmnet", "vnet", "cni", "flannel", "kube", "cali", "lxc", "bond", "dummy", "ifb", "nlmon"):
		return KindVirtual
	case hasAnyPrefix(n, "en", "eth", "em", "eno", "ens", "enp", "enx"):
		return KindEthernet
	default:
		return KindOther
	}
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
