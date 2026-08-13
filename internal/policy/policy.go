// Package policy decides which bearer should carry traffic and, critically,
// when to switch (spec §9, §10, §12). The scoring engine says how good each
// bearer is right now; the policy Controller adds memory — hysteresis — so the
// agent fails over fast when a link genuinely degrades but never oscillates on
// noise.
//
// The controller uses a *relative* model: it rests on its current (preferably
// the "home") bearer and only migrates when another bearer is clearly better by
// a margin, sustained across cycles, and a minimum dwell has elapsed. An
// always-up link never falls below an absolute floor, so relative comparison —
// not a fixed threshold — is what reliably distinguishes "degraded" from "fine".
//
// Sprint 8 adds two anti-flap refinements on top of that base:
//
//   - Recovery hold — after failing away from the preferred bearer, the agent
//     waits for the preferred link to test healthy for several *consecutive*
//     cycles before failing back, so it never bounces home onto a link that has
//     only momentarily recovered.
//   - Flap penalty — the bearer the agent just left enters a short cooldown; a
//     soft, better-path migration back onto it is suppressed until the cooldown
//     expires. A hard loss, or an urgently-bad active link, bypasses the penalty,
//     so smoothness is never bought at the cost of staying on a failing path.
package policy

import "time"

// Class is a traffic class (spec §12).
type Class string

const (
	Critical   Class = "CRITICAL"
	High       Class = "HIGH"
	Normal     Class = "NORMAL"
	Bulk       Class = "BULK"
	Background Class = "BACKGROUND"
)

// Classes returns the built-in traffic classes in priority order.
func Classes() []Class { return []Class{Critical, High, Normal, Bulk, Background} }

// ClassProfile maps a traffic class to the scoring profile used to rank bearers
// for it (spec §8, §12).
func ClassProfile(c Class) string {
	switch c {
	case Critical:
		return "telemetry" // reliability + availability
	case High:
		return "voice" // latency + jitter
	case Bulk, Background:
		return "bulk" // bandwidth + cost
	default:
		return "default"
	}
}

// Scored is a bearer with its computed score for the evaluation at hand.
type Scored struct {
	Name      string
	Score     float64
	Available bool
	Preferred bool // the configured "home" bearer we prefer to rest on
}

// Hysteresis holds the anti-flap parameters (spec §9).
type Hysteresis struct {
	MinImprovement    float64       // a candidate must beat the active bearer by this many points
	FailureThreshold  float64       // active below this is "urgent" — dwell and flap penalty are waived
	RecoveryThreshold float64       // the preferred bearer at/above this counts as "recovered"
	MinDwell          time.Duration // minimum time on a bearer before switching again
	DegradationHold   int           // consecutive degraded cycles required before a soft migrate
	RecoveryHold      int           // consecutive recovered cycles required before failing back home
	FlapPenalty       time.Duration // cooldown before a soft migration back onto a just-left bearer
}

// DefaultHysteresis returns the spec's example defaults (§9).
func DefaultHysteresis() Hysteresis {
	return Hysteresis{
		MinImprovement:    15,
		FailureThreshold:  30,
		RecoveryThreshold: 50,
		MinDwell:          10 * time.Second,
		DegradationHold:   2,
		RecoveryHold:      2,
		FlapPenalty:       20 * time.Second,
	}
}

// Action is the outcome of a decision.
type Action string

const (
	Hold    Action = "HOLD"
	Migrate Action = "MIGRATE"
)

// Decision is what the controller decided this cycle.
type Decision struct {
	Action Action `json:"action"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason"`
}

// Controller carries failover state across cycles and applies hysteresis.
// It is deterministic: the caller supplies the clock, so it is fully testable.
type Controller struct {
	H          Hysteresis
	active     string
	lastSwitch time.Time
	degraded   int                  // consecutive cycles the active bearer has been clearly beaten
	recovered  int                  // consecutive cycles the preferred bearer has tested healthy while off it
	suppressed int                  // soft migrations held back by the flap penalty (observability)
	cooldown   map[string]time.Time // per-bearer earliest time a soft return is allowed again
}

// NewController returns a controller with the given hysteresis parameters.
func NewController(h Hysteresis) *Controller {
	return &Controller{H: h, cooldown: map[string]time.Time{}}
}

// Active returns the currently selected bearer ("" before the first decision).
func (c *Controller) Active() string { return c.active }

// DegradedStreak returns the number of consecutive cycles the active bearer has
// been clearly beaten by a better one (useful for the dashboard / tests).
func (c *Controller) DegradedStreak() int { return c.degraded }

// RecoveredStreak returns the number of consecutive cycles the preferred bearer
// has tested healthy while the agent is failed away from it.
func (c *Controller) RecoveredStreak() int { return c.recovered }

// Suppressed returns how many soft migrations the flap penalty has held back — a
// direct measure of the oscillation the controller has absorbed.
func (c *Controller) Suppressed() int { return c.suppressed }

// Decide evaluates the scored bearers at time now and returns an action,
// updating internal state when it migrates.
func (c *Controller) Decide(now time.Time, bearers []Scored) Decision {
	best := bestAvailable(bearers)
	pref := preferred(bearers)

	// Initial selection: rest on the preferred bearer if it is healthy,
	// otherwise take the best available.
	if c.active == "" {
		if pref != nil && pref.Available && pref.Score >= c.H.RecoveryThreshold {
			c.set(pref.Name, now)
			return Decision{Action: Migrate, To: pref.Name, Reason: "initial path selection (preferred)"}
		}
		if best != nil {
			c.set(best.Name, now)
			return Decision{Action: Migrate, To: best.Name, Reason: "initial path selection"}
		}
		return Decision{Action: Hold, Reason: "no available bearer"}
	}

	act := find(bearers, c.active)

	// Hard loss: the active bearer is gone — migrate immediately (dwell and the
	// flap penalty are both waived). The link that just dropped still earns its
	// own cooldown, so a soft decision cannot bounce straight back onto it.
	if act == nil || !act.Available {
		if best != nil && best.Name != c.active {
			from := c.active
			c.migrateTo(best.Name, now)
			return Decision{Action: Migrate, From: from, To: best.Name, Reason: "active bearer unavailable"}
		}
		c.degraded, c.recovered = 0, 0
		return Decision{Action: Hold, Reason: "active unavailable; no alternative"}
	}

	// Degradation streak: is the active bearer clearly beaten by the best
	// alternative, cycle after cycle?
	degradedNow := best != nil && best.Name != c.active && best.Score-act.Score >= c.H.MinImprovement
	if degradedNow {
		c.degraded++
	} else {
		c.degraded = 0
	}

	// Recovery streak: has the preferred (home) bearer tested healthy for long
	// enough that we can trust a fail-back rather than chase a momentary blip up?
	recoveredNow := pref != nil && pref.Name != c.active && pref.Available && pref.Score >= c.H.RecoveryThreshold
	if recoveredNow {
		c.recovered++
	} else {
		c.recovered = 0
	}

	dwellOK := now.Sub(c.lastSwitch) >= c.H.MinDwell
	urgent := act.Score < c.H.FailureThreshold // active genuinely bad → don't wait out dwell/penalty

	// Fail back to the preferred / home bearer once it has been healthy for the
	// required number of consecutive cycles (recovery hold) and dwell allows.
	if recoveredNow && c.recovered >= c.recoveryHold() && dwellOK {
		from := c.active
		c.migrateTo(pref.Name, now)
		return Decision{Action: Migrate, From: from, To: pref.Name, Reason: "preferred path recovered"}
	}

	// Sustained, clearly-better alternative — migrate, unless the candidate is a
	// bearer we just left that is still inside its flap-penalty cooldown (and the
	// active link is not urgently bad).
	if degradedNow && c.degraded >= c.H.DegradationHold && (dwellOK || urgent) {
		if !urgent && c.inCooldown(best.Name, now) {
			c.suppressed++
			return Decision{Action: Hold, Reason: "better path available but held by flap penalty"}
		}
		from := c.active
		c.migrateTo(best.Name, now)
		return Decision{Action: Migrate, From: from, To: best.Name, Reason: "active degraded; better path available"}
	}

	return Decision{Action: Hold, Reason: "holding current path"}
}

// migrateTo switches the active bearer, putting the bearer we leave into a
// flap-penalty cooldown so a soft migration cannot bounce straight back onto it.
func (c *Controller) migrateTo(name string, now time.Time) {
	if c.active != "" && c.active != name && c.H.FlapPenalty > 0 {
		c.cooldown[c.active] = now.Add(c.H.FlapPenalty)
	}
	c.set(name, now)
}

func (c *Controller) set(name string, now time.Time) {
	c.active = name
	c.lastSwitch = now
	c.degraded = 0
	c.recovered = 0
}

// recoveryHold is the effective recovery-hold count; a value below 1 means fail
// back on the first healthy cycle (the pre-Sprint-8 behaviour).
func (c *Controller) recoveryHold() int {
	if c.H.RecoveryHold < 1 {
		return 1
	}
	return c.H.RecoveryHold
}

// inCooldown reports whether a soft migration back onto name is still penalised.
func (c *Controller) inCooldown(name string, now time.Time) bool {
	until, ok := c.cooldown[name]
	return ok && now.Before(until)
}

func find(bs []Scored, name string) *Scored {
	for i := range bs {
		if bs[i].Name == name {
			return &bs[i]
		}
	}
	return nil
}

func bestAvailable(bs []Scored) *Scored {
	var best *Scored
	for i := range bs {
		if !bs[i].Available {
			continue
		}
		if best == nil || bs[i].Score > best.Score {
			best = &bs[i]
		}
	}
	return best
}

func preferred(bs []Scored) *Scored {
	for i := range bs {
		if bs[i].Preferred {
			return &bs[i]
		}
	}
	return nil
}
