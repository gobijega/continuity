package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/agent"
	"github.com/gobijega/continuity/internal/demo"
	"github.com/gobijega/continuity/internal/policy"
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

func TestScenarioEndpointStatuses(t *testing.T) {
	s, _ := newTestServer(t)
	for _, name := range []string{"dos", "asat", "jamming", "restore"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scenario/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("scenario %s status = %d, want 200", name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scenario/bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown scenario status = %d, want 400", rec.Code)
	}
}

func TestScenarioDisabledInLiveMode(t *testing.T) {
	a := agent.New(agent.Options{Source: agent.NewSimSource(simulator.NewDemo(), "5g")})
	a.Tick(time.Unix(1700000000, 0))
	s := New(a, nil) // no sim -> live mode
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scenario/dos", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("live-mode scenario status = %d, want 409", rec.Code)
	}
}

// TestScenarioSelection is the behavioural check: from a fresh steady state
// (5G active), each attack drives the control loop to autonomously select the
// intended highest-scored surviving bearer.
func TestScenarioSelection(t *testing.T) {
	hyst := policy.Hysteresis{
		MinImprovement: 12, FailureThreshold: 35, RecoveryThreshold: 60,
		MinDwell: 3 * time.Second, DegradationHold: 2, RecoveryHold: 2, FlapPenalty: 6 * time.Second,
	}
	cases := []struct{ scenario, want string }{
		{"dos", "satcom"},   // terrestrial IP flooded -> space layer wins
		{"jamming", "wifi"}, // cellular + SATCOM jammed -> short-range Wi-Fi wins
		{"asat", "5g"},      // satellite destroyed -> agent holds 5G
	}
	for _, c := range cases {
		sim := simulator.NewDemo()
		a := agent.New(agent.Options{Node: "veh", Source: agent.NewSimSource(sim, "5g"), Hyst: hyst})
		s := New(a, sim)
		T := time.Unix(1700000000, 0)
		a.Tick(T) // steady state: 5G active

		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scenario/"+c.scenario, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", c.scenario, rec.Code)
		}
		var active string
		for i := 1; i <= 12; i++ {
			active = a.Tick(T.Add(time.Duration(i) * time.Second)).Active
		}
		if active != c.want {
			t.Errorf("after %s, active = %q, want %q", c.scenario, active, c.want)
		}
	}
}
