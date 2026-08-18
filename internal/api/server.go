// Package api serves the Continuity control and observability surface: the REST
// endpoints from spec §22 plus an embedded single-page dashboard. It reads the
// agent's published snapshot and, in demo mode, forwards simulator controls.
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gobijega/continuity/internal/agent"
	"github.com/gobijega/continuity/internal/demo"
	"github.com/gobijega/continuity/internal/mission"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/simulator"
)

//go:embed dashboard.html
var dashboardHTML []byte

// demoProvider is the observable slice of the scripted demonstration the API
// surfaces. It is satisfied by *demo.Runner.
type demoProvider interface {
	State() demo.State
}

// Server exposes the agent state and (in demo mode) simulator controls.
type Server struct {
	agent   *agent.Agent
	sim     *simulator.Sim  // nil in live mode
	demo    demoProvider    // nil unless the scripted demo is running
	mission *mission.Engine // nil unless the mission engine is attached
	mux     *http.ServeMux
}

// New builds a Server. sim may be nil (live mode), which disables the
// simulator endpoints.
func New(a *agent.Agent, sim *simulator.Sim) *Server {
	s := &Server{agent: a, sim: sim, mux: http.NewServeMux()}
	s.routes()
	return s
}

// SetDemo attaches a scripted-demo provider so its narrative is published on
// /api/v1/state and /api/v1/demo.
func (s *Server) SetDemo(d demoProvider) { s.demo = d }

// SetMission attaches the Mission Context Engine so the API can switch mission
// profile / state and apply mission scenario presets.
func (s *Server) SetMission(e *mission.Engine) { s.mission = e }

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.HandleFunc("GET /api/v1/state", s.handleState)
	s.mux.HandleFunc("GET /api/v1/node", s.handleNode)
	s.mux.HandleFunc("GET /api/v1/interfaces", s.handleInterfaces)
	s.mux.HandleFunc("GET /api/v1/interfaces/{name}", s.handleInterface)
	s.mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	s.mux.HandleFunc("GET /api/v1/policy", s.handlePolicy)
	s.mux.HandleFunc("GET /api/v1/tunnel", s.handleTunnel)
	s.mux.HandleFunc("GET /api/v1/demo", s.handleDemo)
	s.mux.HandleFunc("GET /api/v1/decisions", s.handleDecisions)
	s.mux.HandleFunc("POST /api/v1/simulator/{name}/{action}", s.handleSimulator)
	s.mux.HandleFunc("POST /api/v1/scenario/{name}", s.handleScenario)
	s.mux.HandleFunc("POST /api/v1/mission/profile/{profile}", s.handleMissionProfile)
	s.mux.HandleFunc("POST /api/v1/mission/state/{state}", s.handleMissionState)
	s.mux.HandleFunc("POST /api/v1/mission/scenario/{name}", s.handleMissionScenario)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(dashboardHTML)
}

// stateResp is the full snapshot plus a flag telling the dashboard whether the
// simulator controls are live and, when running, the scripted-demo narrative.
type stateResp struct {
	agent.Snapshot
	Sim  bool        `json:"sim"`
	Demo *demo.State `json:"demo,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	resp := stateResp{Snapshot: s.agent.Snapshot(), Sim: s.sim != nil}
	if s.demo != nil {
		d := s.demo.State()
		resp.Demo = &d
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	snap := s.agent.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              snap.Node,
		"status":          strings.ToLower(snap.Status),
		"activeInterface": snap.Active,
	})
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agent.Snapshot().Bearers)
}

func (s *Server) handleInterface(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, b := range s.agent.Snapshot().Bearers {
		if b.Name == name {
			writeJSON(w, http.StatusOK, b)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "interface not found"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agent.Snapshot().Events)
}

func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	snap := s.agent.Snapshot()
	classes := make(map[string]string, len(policy.Classes()))
	for _, c := range policy.Classes() {
		classes[string(c)] = policy.ClassProfile(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": snap.Profile, "classes": classes})
}

// handleTunnel reports the session-continuity overlay: its stable address, the
// bearer it currently egresses through, and how many failovers it has ridden
// out without dropping the session (spec §13, Sprint 9).
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agent.Continuity())
}

// handleDemo reports the scripted-demonstration narrative (Sprint 10), or an
// idle state when no demo is attached.
func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	if s.demo == nil {
		writeJSON(w, http.StatusOK, demo.State{Running: false})
		return
	}
	writeJSON(w, http.StatusOK, s.demo.State())
}

// handleSimulator applies a demo impairment. Actions: degrade | outage |
// restore. name "all" with restore clears every bearer (spec §22, dev-mode).
func (s *Server) handleSimulator(w http.ResponseWriter, r *http.Request) {
	if s.sim == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "simulator not enabled (live mode)"})
		return
	}
	name := r.PathValue("name")
	action := r.PathValue("action")
	switch action {
	case "degrade":
		s.sim.Degrade(name, 400, 60, 28, 0.1)
	case "outage":
		s.sim.Outage(name)
	case "restore":
		if name == "all" {
			for _, n := range s.sim.Names() {
				s.sim.Restore(n)
			}
		} else {
			s.sim.Restore(name)
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": action, "target": name})
}

// handleScenario applies a named adversarial scenario (dos | asat | jamming |
// restore) to the simulator. It hands manual control to the visitor: the
// scripted demo is paused so it stops reshaping the simulator, the attack is
// applied, and the agent re-selects the highest-scored surviving bearer.
// "restore" resumes the ambient demonstration.
func (s *Server) handleScenario(w http.ResponseWriter, r *http.Request) {
	if s.sim == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "simulator not enabled (live mode)"})
		return
	}
	name := r.PathValue("name")
	fn, ok := simulator.Attacks()[name]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown scenario"})
		return
	}
	// Pause/resume the scripted demo if one is attached, so a visitor's manual
	// scenario isn't overwritten by the loop on the next tick.
	if d, ok := s.demo.(interface {
		Pause(time.Time)
		Resume(time.Time)
	}); ok {
		now := time.Now()
		if name == "restore" {
			d.Resume(now)
		} else {
			d.Pause(now)
		}
	}
	fn(s.sim)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "scenario", "scenario": name})
}

// handleMissionProfile switches the mission profile (spec §3). The control loop
// picks up the change on its next tick and re-scores every bearer.
func (s *Server) handleMissionProfile(w http.ResponseWriter, r *http.Request) {
	if s.mission == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "mission engine not enabled"})
		return
	}
	p := mission.Profile(strings.ToUpper(strings.TrimSpace(r.PathValue("profile"))))
	if !s.mission.SetProfile(p) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mission profile"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "profile", "profile": string(p)})
}

// handleMissionState switches the operational mission state (spec §4).
func (s *Server) handleMissionState(w http.ResponseWriter, r *http.Request) {
	if s.mission == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "mission engine not enabled"})
		return
	}
	st := mission.State(strings.ToUpper(strings.TrimSpace(r.PathValue("state"))))
	if !s.mission.SetState(st) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mission state"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "state", "state": string(st)})
}

// handleMissionScenario applies a one-click mission preset (spec §10): it sets
// the scenario's profile + state and shapes the simulated environment. It pauses
// the scripted ambient demo (if attached) so the preset isn't overwritten on the
// next beat, exactly like the adversarial scenarios.
func (s *Server) handleMissionScenario(w http.ResponseWriter, r *http.Request) {
	if s.mission == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "mission engine not enabled"})
		return
	}
	name := r.PathValue("name")
	sc, ok := mission.ScenarioByID(name)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mission scenario"})
		return
	}
	if d, ok := s.demo.(interface{ Pause(time.Time) }); ok {
		d.Pause(time.Now())
	}
	s.mission.Set(sc.Profile, sc.State)
	if s.sim != nil {
		if shape := missionSimShape(name); shape != nil {
			shape(s.sim)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "mission-scenario", "scenario": name,
		"profile": string(sc.Profile), "state": string(sc.State)})
}

// missionSimShape returns the simulated network conditions for a mission
// scenario. The override / critical-traffic scenarios deliberately leave the
// network healthy so the change in decision is attributable to mission policy
// alone, not to a change in conditions.
func missionSimShape(name string) func(*simulator.Sim) {
	switch name {
	case "routine-mobility":
		return func(s *simulator.Sim) { s.RestoreAll(); s.Degrade("wifi", 40, 30, 14, 0.7) }
	case "coverage-loss":
		return func(s *simulator.Sim) { s.RestoreAll(); s.Degrade("5g", 320, 45, 18, 0.35) }
	case "mission-priority-override":
		return func(s *simulator.Sim) { s.RestoreAll() }
	case "critical-traffic-event":
		return func(s *simulator.Sim) { s.RestoreAll() }
	case "contested":
		return func(s *simulator.Sim) {
			s.RestoreAll()
			s.Degrade("5g", 300, 55, 22, 0.30)
			s.Degrade("satcom", 120, 30, 8, 0.60)
		}
	}
	return nil
}

// handleDecisions exports the mission-aware decision log (spec §17) as JSON, or
// CSV with ?format=csv, so decisions can be replayed and quantitatively tested.
func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	recs := s.agent.Decisions()
	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=continuity-decisions.csv")
		fmt.Fprintln(w, "time,mission_profile,mission_state,active_class,active,network_pick,mission_pick,override,action,from,to,influence_pct,influence_band,network_event,reason")
		for _, d := range recs {
			fmt.Fprintf(w, "%s,%s,%s,%q,%s,%s,%s,%t,%s,%s,%s,%.1f,%s,%q,%q\n",
				d.At.UTC().Format(time.RFC3339), d.MissionProfile, d.MissionState, d.ActiveClass,
				d.Active, d.NetworkPick, d.MissionPick, d.Override, d.Action, d.From, d.To,
				d.InfluencePct, d.InfluenceBand, d.NetworkEvent, d.Reason)
		}
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
