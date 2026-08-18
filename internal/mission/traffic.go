package mission

// This file models application / traffic classes (spec §5). Continuity does not
// select a network globally: different traffic has different mission value, and
// the mission state decides how each class is treated — prioritised, carried
// normally, throttled, deferred or suspended.

// Priority is a traffic class's standing mission value.
type Priority string

const (
	PriCritical   Priority = "CRITICAL"
	PriHigh       Priority = "HIGH"
	PriMedium     Priority = "MEDIUM"
	PriLow        Priority = "LOW"
	PriDeferrable Priority = "DEFERRABLE"
)

// Outcome is the policy applied to a traffic class in the current mission state.
type Outcome string

const (
	Prioritise Outcome = "PRIORITISE"
	Normal     Outcome = "NORMAL"
	Throttle   Outcome = "THROTTLE"
	Defer      Outcome = "DEFER"
	Suspend    Outcome = "SUSPEND"
)

// TrafficClass is one simulated application/traffic class. profile is the
// scoring application profile it implies (used when this class is the dominant
// traffic driving bearer weighting).
type TrafficClass struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Priority Priority `json:"priority"`
	profile  string
}

// Profile returns the scoring application profile this class implies.
func (c TrafficClass) Profile() string { return c.profile }

// TrafficClasses returns the built-in traffic classes in descending priority.
func TrafficClasses() []TrafficClass {
	return []TrafficClass{
		{ID: "c2", Name: "Command & Control", Priority: PriCritical, profile: "telemetry"},
		{ID: "safety", Name: "Safety / Emergency", Priority: PriCritical, profile: "telemetry"},
		{ID: "telemetry", Name: "Telemetry", Priority: PriHigh, profile: "telemetry"},
		{ID: "voice", Name: "Voice", Priority: PriHigh, profile: "voice"},
		{ID: "opdata", Name: "Operational Data", Priority: PriMedium, profile: "default"},
		{ID: "video", Name: "Video / ISR Stream", Priority: PriMedium, profile: "video"},
		{ID: "bulk", Name: "Bulk Data Transfer", Priority: PriLow, profile: "bulk"},
		{ID: "bgsync", Name: "Software Update / Background Sync", Priority: PriDeferrable, profile: "bulk"},
	}
}

// classByID looks a class up by id (falls back to Operational Data).
func classByID(id string) TrafficClass {
	for _, c := range TrafficClasses() {
		if c.ID == id {
			return c
		}
	}
	return TrafficClass{ID: "opdata", Name: "Operational Data", Priority: PriMedium, profile: "default"}
}

// TrafficDecision is a class plus the outcome the current mission state assigns
// to it — the row rendered in the traffic-policy panel.
type TrafficDecision struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Priority Priority `json:"priority"`
	Outcome  Outcome  `json:"outcome"`
}

// outcomeMatrix is the deterministic mapping of (state, class) → outcome. It
// escalates monotonically NORMAL < ELEVATED < DEGRADED < CRITICAL: critical and
// safety traffic is prioritised as soon as the state lifts, while lower-value
// traffic is progressively throttled, deferred and finally suspended. The
// CRITICAL column matches the spec §5 worked example exactly.
var outcomeMatrix = map[State]map[string]Outcome{
	StateNormal: {
		"c2": Normal, "safety": Normal, "telemetry": Normal, "voice": Normal,
		"opdata": Normal, "video": Normal, "bulk": Normal, "bgsync": Normal,
	},
	StateElevated: {
		"c2": Prioritise, "safety": Prioritise, "telemetry": Prioritise, "voice": Normal,
		"opdata": Normal, "video": Normal, "bulk": Throttle, "bgsync": Defer,
	},
	StateDegraded: {
		"c2": Prioritise, "safety": Prioritise, "telemetry": Prioritise, "voice": Normal,
		"opdata": Normal, "video": Throttle, "bulk": Throttle, "bgsync": Defer,
	},
	StateCritical: {
		"c2": Prioritise, "safety": Prioritise, "telemetry": Prioritise, "voice": Normal,
		"opdata": Throttle, "video": Throttle, "bulk": Defer, "bgsync": Suspend,
	},
}

// TrafficPolicy returns the outcome for every traffic class in a given mission
// state, in priority order.
func TrafficPolicy(st State) []TrafficDecision {
	row, ok := outcomeMatrix[st]
	if !ok {
		row = outcomeMatrix[StateNormal]
	}
	classes := TrafficClasses()
	out := make([]TrafficDecision, 0, len(classes))
	for _, c := range classes {
		oc, ok := row[c.ID]
		if !ok {
			oc = Normal
		}
		out = append(out, TrafficDecision{ID: c.ID, Name: c.Name, Priority: c.Priority, Outcome: oc})
	}
	return out
}

// dominantClassID is the traffic class that best represents what the node is
// carrying in a given state, and whose application profile is folded into the
// bearer weighting. Routine states carry mixed operational data; as the state
// escalates, telemetry and then command & control dominate.
func dominantClassID(st State) string {
	switch st {
	case StateCritical, StateDegraded:
		return "c2"
	case StateElevated:
		return "telemetry"
	default:
		return "opdata"
	}
}

// DominantClass returns the dominant traffic class for a state.
func DominantClass(st State) TrafficClass { return classByID(dominantClassID(st)) }
