package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/agent"
	"github.com/gobijega/continuity/internal/demo"
	"github.com/gobijega/continuity/internal/simulator"
)

func newTestServer(t *testing.T) (*Server, *agent.Agent) {
	t.Helper()
	sim := simulator.NewDemo()
	a := agent.New(agent.Options{Node: "vehicle-01", Source: agent.NewSimSource(sim, "5g")})
	a.Tick(time.Unix(1700000000, 0)) // populate a snapshot
	return New(a, sim), a
}

func TestStateEndpoint(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want 200", rec.Code)
	}
	var resp struct {
		Node    string `json:"node"`
		Sim     bool   `json:"sim"`
		Bearers []struct {
			Name string `json:"name"`
		} `json:"bearers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Node != "vehicle-01" || !resp.Sim || len(resp.Bearers) != 3 {
		t.Fatalf("unexpected state: %+v", resp)
	}
}

func TestNodeAndInterface(t *testing.T) {
	s, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/node", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("node status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/interfaces/5g", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("interface 5g status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/interfaces/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown interface status = %d, want 404", rec.Code)
	}
}

func TestTunnelEndpoint(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/tunnel", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tunnel status = %d, want 200", rec.Code)
	}
	var resp struct {
		Enabled bool   `json:"enabled"`
		Overlay string `json:"overlay"`
		Cipher  string `json:"cipher"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Enabled || resp.Overlay == "" || resp.Cipher == "" {
		t.Fatalf("unexpected tunnel state: %+v", resp)
	}
}

func TestDemoEndpoint(t *testing.T) {
	s, _ := newTestServer(t)

	// No demo attached -> idle.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/demo", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("demo status = %d, want 200", rec.Code)
	}
	var idle struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &idle); err != nil {
		t.Fatal(err)
	}
	if idle.Running {
		t.Error("demo should be idle before SetDemo")
	}

	// Attach a running demo; the endpoint should reflect it.
	r := demo.New(simulator.NewDemo())
	r.Start(time.Unix(1700000000, 0))
	s.SetDemo(r)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/demo", nil))
	var st struct {
		Running bool `json:"running"`
		Steps   int  `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Steps == 0 {
		t.Fatalf("expected a running demo with steps, got %+v", st)
	}
}

func TestSimulatorEndpoint(t *testing.T) {
	s, a := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/simulator/5g/degrade", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("degrade status = %d, want 200", rec.Code)
	}
	// After a degrade, a tick should reflect worse 5G metrics.
	snap := a.Tick(time.Unix(1700000005, 0))
	for _, b := range snap.Bearers {
		if b.Name == "5g" && b.Metrics.LossPct == 0 {
			t.Error("expected 5g to show loss after degrade")
		}
	}
}

func TestSimulatorDisabledInLiveMode(t *testing.T) {
	a := agent.New(agent.Options{Source: agent.NewSimSource(simulator.NewDemo(), "5g")})
	a.Tick(time.Unix(1700000000, 0))
	s := New(a, nil) // no sim -> live mode
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/simulator/5g/degrade", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("live-mode simulator status = %d, want 409", rec.Code)
	}
}
