package mission

import "strings"

// BearerType is a coarse, mission-relevant classification of a bearer. It is
// derived from the bearer's name and link kind and drives two mission inputs:
// the structural reliability/resilience characteristics fed into scoring, and
// the per-profile suitability multiplier that lets mission policy prefer or
// penalise a bearer type independently of its live metrics.
type BearerType string

const (
	Satellite BearerType = "satellite"
	Cellular  BearerType = "cellular"
	WiFi      BearerType = "wifi"
	Wired     BearerType = "wired"
	Radio     BearerType = "radio"
	OtherType BearerType = "other"
)

// ClassifyBearer maps a bearer name + link kind onto a mission BearerType. Name
// is checked first (a link named "satcom" is satellite even though the demo
// models it over a tunnel device), then the kind.
func ClassifyBearer(name, kind string) BearerType {
	n := strings.ToLower(name)
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(n, "sat") || strings.Contains(k, "satellite"):
		return Satellite
	case containsAny(n, "radio", "tac", "hf", "vhf", "uhf", "mesh", "manet") || k == "radio":
		return Radio
	case k == "cellular" || containsAny(n, "5g", "4g", "lte", "cell", "wwan", "modem"):
		return Cellular
	case k == "wifi" || containsAny(n, "wifi", "wlan", "wl"):
		return WiFi
	case k == "ethernet" || k == "wired" || containsAny(n, "eth", "wired", "lan", "fiber", "fibre"):
		return Wired
	case k == "tunnel":
		// A tunnel not named like a satellite is treated as an opaque overlay.
		return OtherType
	default:
		return OtherType
	}
}

// Characteristics returns the SIMULATED structural reliability and resilience
// (both 0..100) of a bearer type. These are deliberately static properties of
// the transport class — how inherently dependable and hard-to-deny it is —
// distinct from the live latency/loss/jitter the telemetry layer measures.
// A hardened SATCOM or frequency-agile tactical radio is structurally resilient
// even when momentarily slow; a contended cellular or short-range Wi-Fi link is
// less so even when momentarily fast. Representative demonstrator values, not
// measured or validated figures.
func Characteristics(bt BearerType) (reliability, resilience float64) {
	switch bt {
	case Satellite:
		return 99, 92
	case Wired:
		return 99, 96
	case Radio:
		return 97, 90
	case Cellular:
		return 95, 70
	case WiFi:
		return 90, 58
	default:
		return 92, 75
	}
}

// baseSuitability is the per-profile preference multiplier for each bearer type
// (1.0 = neutral). It encodes each mission profile's doctrine about which
// transports best serve the objective: routine ops favour economical
// high-capacity terrestrial links; assured/emergency ops favour the most stable
// and available transports; the contested profile applies a simulated
// bearer-risk policy that penalises high-emission, infrastructure-dependent
// bearers and favours diverse, low-observable ones.
var baseSuitability = map[Profile]map[BearerType]float64{
	Routine: {
		Satellite: 0.98, Cellular: 1.05, WiFi: 1.02, Wired: 1.06, Radio: 0.90, OtherType: 1.00,
	},
	MissionCritical: {
		Satellite: 1.10, Cellular: 1.00, WiFi: 0.90, Wired: 1.12, Radio: 1.06, OtherType: 1.00,
	},
	Emergency: {
		Satellite: 1.14, Cellular: 0.95, WiFi: 0.82, Wired: 1.15, Radio: 1.10, OtherType: 1.00,
	},
	Contested: {
		Satellite: 0.85, Cellular: 0.72, WiFi: 0.92, Wired: 1.12, Radio: 1.08, OtherType: 0.95,
	},
}

// Suitability returns the mission suitability multiplier for a bearer type under
// a given profile and state. The profile sets the base preference; escalating
// state applies a bounded extra tilt toward the structurally resilient
// transports (satellite, wired, radio) and away from the more contended ones.
// The result is clamped to [0.6, 1.25] so a single multiplier can never wholly
// override the network score — it shades it.
func Suitability(p Profile, st State, bt BearerType) float64 {
	base, ok := baseSuitability[p][bt]
	if !ok {
		base = 1.0
	}
	base *= stateSuitabilityTilt(st, bt)
	return clampF(base, 0.6, 1.25)
}

// stateSuitabilityTilt nudges suitability by operational state: as the state
// escalates, resilient transports are favoured a little more and contended ones
// a little less. NORMAL is neutral.
func stateSuitabilityTilt(st State, bt BearerType) float64 {
	var step float64
	switch st {
	case StateElevated:
		step = 0.02
	case StateDegraded:
		step = 0.05
	case StateCritical:
		step = 0.08
	default:
		return 1.0
	}
	switch bt {
	case Satellite, Wired, Radio:
		return 1 + step
	case Cellular, WiFi:
		return 1 - step
	default:
		return 1.0
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func clampF(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
