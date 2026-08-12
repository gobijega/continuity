package simulator

import (
	"testing"
	"time"
)

var t0 = time.Unix(1700000000, 0)

func TestDemoBaseline(t *testing.T) {
	s := NewDemo()
	m := s.Sample("5g", t0)
	if !m.OK || m.LossPct != 0 || m.LatencyMs != 28 {
		t.Fatalf("5g baseline = %+v, want healthy (28ms, 0%% loss)", m)
	}
	if len(s.Names()) != 3 {
		t.Fatalf("expected 3 bearers, got %v", s.Names())
	}
}

func TestDegradeAndRestore(t *testing.T) {
	s := NewDemo()
	s.Degrade("5g", 380, 40, 20, 0.2)
	if m := s.Sample("5g", t0); m.LossPct != 20 || m.LatencyMs != 28+380 {
		t.Fatalf("degraded 5g = %+v, want loss 20 and latency 408", m)
	}
	s.Restore("5g")
	if m := s.Sample("5g", t0); m.LossPct != 0 || m.LatencyMs != 28 {
		t.Fatalf("restored 5g = %+v, want baseline", m)
	}
}

func TestOutage(t *testing.T) {
	s := NewDemo()
	s.Outage("satcom")
	if m := s.Sample("satcom", t0); m.OK || m.LossPct != 100 {
		t.Fatalf("outage satcom = %+v, want down (100%% loss, not OK)", m)
	}
}

func TestScenarioApplies(t *testing.T) {
	s := NewDemo()
	Scenarios()["CELLULAR_CONGESTION"](s)
	if m := s.Sample("5g", t0); m.LossPct == 0 {
		t.Fatal("CELLULAR_CONGESTION should have degraded 5g")
	}
}

func TestNetemArgs(t *testing.T) {
	add := NetemAddArgs("wwan0", 400, 40, 20)
	if add[0] != "qdisc" || add[3] != "wwan0" {
		t.Fatalf("NetemAddArgs = %v", add)
	}
	if clr := NetemClearArgs("wwan0"); clr[0] != "qdisc" || clr[1] != "del" {
		t.Fatalf("NetemClearArgs = %v", clr)
	}
}
