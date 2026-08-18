package agent

// This file is the agent's mission-aware layer. When an Agent is given a
// mission.Engine, each cycle it scores every bearer three ways —
//
//	network score      : a conventional connectivity-first ranking (the
//	                     "network-only recommendation")
//	mission score      : the same metrics re-weighted by mission policy, plus a
//	                     structural resilience term
//	effective score    : mission score × the bearer's mission suitability, the
//	                     value the policy controller actually selects on
//
// — and records the full, explainable decision. Keeping it here keeps agent.go's
// control loop readable and the mission logic separately testable.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gobijega/continuity/internal/mission"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/scoring"
)

// DecisionScore is one bearer's score breakdown recorded for a decision.
type DecisionScore struct {
	Network   float64 `json:"network"`
	Mission   float64 `json:"mission"`
	Suit      float64 `json:"suitability"`
	Effective float64 `json:"effective"`
}

// DecisionRecord captures one decision cycle for instrumentation and export
// (spec §17), so decisions can be replayed and quantitatively tested later.
type DecisionRecord struct {
	At             time.Time                `json:"at"`
	MissionProfile string                   `json:"mission_profile"`
	MissionState   string                   `json:"mission_state"`
	ActiveClass    string                   `json:"active_class"`
	Active         string                   `json:"active"`
	Candidate      string                   `json:"candidate"`
	NetworkPick    string                   `json:"network_pick"`
	MissionPick    string                   `json:"mission_pick"`
	Override       bool                     `json:"override"`
	Action         string                   `json:"action"`
	From           string                   `json:"from,omitempty"`
	To             string                   `json:"to,omitempty"`
	Reason         string                   `json:"reason"`
	InfluencePct   float64                  `json:"influence_pct"`
	InfluenceBand  string                   `json:"influence_band"`
	NetworkEvent   string                   `json:"network_event,omitempty"`
	Scores         map[string]DecisionScore `json:"scores"`
}

// HystView is the live hysteresis explanation for the dashboard (spec §12):
// current vs candidate bearer, the score gap, the switching threshold in force
// (mission-modulated), dwell, streaks and the resulting decision.
type HystView struct {
	Current         string  `json:"current"`
	Candidate       string  `json:"candidate"`
	ScoreDiff       float64 `json:"score_diff"`
	Threshold       float64 `json:"threshold"`
	DwellS          float64 `json:"dwell_s"`
	DegradedStreak  int     `json:"degraded_streak"`
	RecoveredStreak int     `json:"recovered_streak"`
	Suppressed      int     `json:"suppressed"`
	Decision        string  `json:"decision"`
	Note            string  `json:"note"`
}

// MissionView is the mission layer published on the snapshot. It embeds the
// mission.Context (profile, state, weights, traffic policy, constraints) and
// adds the comparison that proves the thesis: the network-only pick vs the
// mission-aware pick, whether mission overrode the network, how strong that
// influence was, and the plain-language reasons.
type MissionView struct {
	mission.Context
	NetworkPick   string   `json:"network_pick"`
	MissionPick   string   `json:"mission_pick"`
	Override      bool     `json:"override"`
	InfluencePct  float64  `json:"influence_pct"`
	InfluenceBand string   `json:"influence_band"`
	Reasons       []string `json:"reasons"`
	HystState     HystView `json:"hysteresis_state"`
}

// missionScored is the per-cycle result of mission scoring.
type missionScored struct {
	views    []BearerView
	scored   []policy.Scored
	suit     map[string]float64
	scores   map[string]DecisionScore
	netPick  string
	missPick string
}

// computeMission scores every reading under the mission context, producing the
// bearer views, the policy inputs (on the effective score), the suitability map
// and the network-only vs mission-aware picks.
func computeMission(readings []Reading, mctx mission.Context) missionScored {
	res := missionScored{
		suit:   make(map[string]float64, len(readings)),
		scores: make(map[string]DecisionScore, len(readings)),
	}
	var bestNet, bestEff float64
	for _, r := range readings {
		bt := mission.ClassifyBearer(r.Name, r.Kind)
		rel, resil := mission.Characteristics(bt)
		in := scoring.Input{
			Available:      r.Metrics.OK,
			LatencyMs:      r.Metrics.LatencyMs,
			JitterMs:       r.Metrics.JitterMs,
			LossPct:        r.Metrics.LossPct,
			ThroughputMbps: r.Metrics.ThroughputMbps,
			ReliabilityPct: rel,
			ResiliencePct:  resil,
			CostScore:      costScore(r.Kind),
		}
		netScore := scoring.Compute(in, mctx.NetworkWeights, mctx.Thresh).Total
		mis := scoring.Compute(in, mctx.Weights, mctx.Thresh)
		suit := mission.Suitability(mctx.Profile, mctx.State, bt)
		eff := clampScore(mis.Total * suit)
		if !r.Metrics.OK {
			eff = 0
		}

		res.suit[r.Name] = suit
		res.scores[r.Name] = DecisionScore{Network: netScore, Mission: mis.Total, Suit: round2(suit), Effective: eff}
		res.views = append(res.views, BearerView{
			Name:           r.Name,
			Kind:           r.Kind,
			Metrics:        r.Metrics,
			Score:          eff,
			Components:     mis.Components,
			Preferred:      r.Preferred,
			BearerType:     string(bt),
			NetworkScore:   netScore,
			MissionScore:   mis.Total,
			Suitability:    round2(suit),
			EffectiveScore: eff,
		})
		res.scored = append(res.scored, policy.Scored{Name: r.Name, Score: eff, Available: r.Metrics.OK, Preferred: r.Preferred})

		if r.Metrics.OK && netScore > bestNet {
			bestNet, res.netPick = netScore, r.Name
		}
		if r.Metrics.OK && eff > bestEff {
			bestEff, res.missPick = eff, r.Name
		}
	}
	// The bearer the mission wants to rest on becomes the controller's "home".
	// This is what keeps mission awareness and hysteresis cooperating rather than
	// fighting: the controller converges to the mission-preferred path (via the
	// recovery-hold), then holds it, instead of failing back to a statically
	// configured home the mission no longer favours. When the mission pick
	// changes, the home moves with it and the switch is gated by recovery-hold +
	// dwell, so it is deliberate, not oscillatory.
	for i := range res.scored {
		res.scored[i].Preferred = res.scored[i].Name == res.missPick
	}
	return res
}

// buildReasons produces the "why Continuity chose this bearer" codes (spec §8)
// from the mission context and the chosen vs network-fastest bearers.
func buildReasons(mctx mission.Context, chosen string, ms missionScored, override bool, d policy.Decision) []string {
	reasons := []string{
		"mission state: " + string(mctx.State),
		"traffic priority: " + strings.ToUpper(mctx.ActiveClass.Name),
	}
	if mctx.Profile == mission.Routine {
		reasons = append(reasons, "throughput and latency weighting favoured (routine ops)")
	} else {
		reasons = append(reasons, "reliability and resilience weighting increased", "throughput weighting reduced")
	}
	if mctx.Profile == mission.Contested {
		reasons = append(reasons, "simulated bearer-risk policy applied to bearer preference")
	}
	if chosen != "" && ms.netPick != "" && chosen != ms.netPick {
		reasons = append(reasons, fmt.Sprintf("%s structurally more resilient than the network-fastest %s",
			strings.ToUpper(chosen), strings.ToUpper(ms.netPick)))
	}
	if override {
		reasons = append(reasons, "mission policy overrides the network-only recommendation")
	} else {
		reasons = append(reasons, "mission-aware and network-only recommendations agree")
	}
	reasons = append(reasons, hystReason(d))
	return reasons
}

func hystReason(d policy.Decision) string {
	if d.Action == policy.Migrate {
		return "switching threshold satisfied — migrating"
	}
	return "hysteresis holding current path"
}

// buildHyst assembles the live hysteresis explanation from the controller state
// and the effective scores.
func buildHyst(active string, ms missionScored, h policy.Hysteresis, degraded, recovered, suppressed int, d policy.Decision, note string) HystView {
	cand, diff := candidateAgainst(active, ms)
	return HystView{
		Current:         active,
		Candidate:       cand,
		ScoreDiff:       round1(diff),
		Threshold:       round1(h.MinImprovement),
		DwellS:          h.MinDwell.Seconds(),
		DegradedStreak:  degraded,
		RecoveredStreak: recovered,
		Suppressed:      suppressed,
		Decision:        string(d.Action),
		Note:            note,
	}
}

// candidateAgainst returns the best effective-scored bearer other than active
// and its score advantage over the active bearer.
func candidateAgainst(active string, ms missionScored) (string, float64) {
	var actScore, bestOther float64
	var cand string
	for _, v := range ms.views {
		if v.Name == active {
			actScore = v.EffectiveScore
		}
	}
	for _, v := range ms.views {
		if v.Name == active || !v.Metrics.OK {
			continue
		}
		if cand == "" || v.EffectiveScore > bestOther {
			cand, bestOther = v.Name, v.EffectiveScore
		}
	}
	if cand == "" {
		return "—", 0
	}
	return cand, bestOther - actScore
}

// decisionLog is a bounded ring buffer of DecisionRecords (spec §17).
type decisionLog struct {
	mu  sync.Mutex
	buf []DecisionRecord
	max int
}

func newDecisionLog(max int) *decisionLog {
	if max <= 0 {
		max = 300
	}
	return &decisionLog{max: max}
}

func (l *decisionLog) add(r DecisionRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, r)
	if len(l.buf) > l.max {
		l.buf = l.buf[len(l.buf)-l.max:]
	}
}

// recent returns up to n most-recent records, newest last.
func (l *decisionLog) recent(n int) []DecisionRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.buf) {
		n = len(l.buf)
	}
	out := make([]DecisionRecord, n)
	copy(out, l.buf[len(l.buf)-n:])
	return out
}

// all returns a copy of every retained record (for export).
func (l *decisionLog) all() []DecisionRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionRecord, len(l.buf))
	copy(out, l.buf)
	return out
}

func clampScore(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 100 {
		return 100
	}
	return x
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
