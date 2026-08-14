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

func TestAttacks(t *testing.T) {
	a := Attacks()
	for _, name := range []string{"dos", "asat", "jamming", "restore"} {
		if a[name] == nil {
			t.Fatalf("missing attack %q", name)
		}
	}

	// DoS floods the terrestrial IP bearers; SATCOM stays off the flooded route.
	s := NewDemo()
	a["dos"](s)
	if m := s.Sample("satcom", t0); !m.OK || m.LossPct != 0 {
		t.Errorf("dos: satcom should stay clean, got %+v", m)
	}
	if m := s.Sample("5g", t0); m.LossPct == 0 {
		t.Error("dos: 5g should be flooded")
	}
	if m := s.Sample("wifi", t0); m.LossPct <= 8 {
		t.Error("dos: wifi should be flooded above its 8% baseline")
	}

	// ASAT removes SATCOM; terrestrial bearers untouched.
	s = NewDemo()
	a["asat"](s)
	if m := s.Sample("satcom", t0); m.OK {
		t.Error("asat: satcom should be down")
	}
	if m := s.Sample("5g", t0); !m.OK || m.LossPct != 0 {
		t.Errorf("asat: 5g should be untouched, got %+v", m)
	}

	// Jamming hits cellular + SATCOM hardest; short-range Wi-Fi least.
	s = NewDemo()
	a["jamming"](s)
	fiveG, sat, wifi := s.Sample("5g", t0), s.Sample("satcom", t0), s.Sample("wifi", t0)
	if !wifi.OK || wifi.LossPct >= fiveG.LossPct || wifi.LossPct >= sat.LossPct {
		t.Errorf("jamming: wifi should be least affected (wifi=%.0f 5g=%.0f sat=%.0f loss)",
			wifi.LossPct, fiveG.LossPct, sat.LossPct)
	}

	// Restore clears everything back to baseline.
	a["restore"](s)
	if m := s.Sample("5g", t0); m.LossPct != 0 || m.LatencyMs != 28 {
		t.Errorf("restore: 5g should be baseline, got %+v", m)
	}
}
