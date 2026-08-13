// Package api serves the Continuity control and observability surface: the REST
// endpoints from spec §22 plus an embedded single-page dashboard. It reads the
// agent's published snapshot and, in demo mode, forwards simulator controls.
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gobijega/continuity/internal/agent"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/simulator"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Server exposes the agent state and (in demo mode) simulator controls.
type Server struct {
	agent *agent.Agent
	sim   *simulator.Sim // nil in live mode
	mux   *http.ServeMux
}

// New builds a Server. sim may be nil (live mode), which disables the
// simulator endpoints.
func New(a *agent.Agent, sim *simulator.Sim) *Server {
	s := &Server{agent: a, sim: sim, mux: http.NewServeMux()}
	s.routes()
	return s
}

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
	s.mux.HandleFunc("POST /api/v1/simulator/{name}/{action}", s.handleSimulator)
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
// simulator controls are live.
type stateResp struct {
	agent.Snapshot
	Sim bool `json:"sim"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, stateResp{Snapshot: s.agent.Snapshot(), Sim: s.sim != nil})
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
