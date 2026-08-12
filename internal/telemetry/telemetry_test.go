package telemetry

import (
	"context"
	"errors"
	"math"
	"net"
	"testing"
	"time"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMean(t *testing.T) {
	if got := Mean([]float64{1, 2, 3, 4}); !almost(got, 2.5) {
		t.Errorf("Mean = %v, want 2.5", got)
	}
	if got := Mean(nil); got != 0 {
		t.Errorf("Mean(nil) = %v, want 0", got)
	}
}

func TestStdDev(t *testing.T) {
	if got := StdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9}); !almost(got, 2) {
		t.Errorf("StdDev = %v, want 2", got)
	}
	if got := StdDev([]float64{5}); got != 0 {
		t.Errorf("StdDev(single) = %v, want 0", got)
	}
}

func TestJitter(t *testing.T) {
	// consecutive diffs: 8,7,3 -> mean 6
	if got := Jitter([]float64{10, 18, 11, 14}); !almost(got, 6) {
		t.Errorf("Jitter = %v, want 6", got)
	}
	if got := Jitter([]float64{42}); got != 0 {
		t.Errorf("Jitter(single) = %v, want 0", got)
	}
}

func TestLossPercent(t *testing.T) {
	if got := LossPercent(1, 4); !almost(got, 25) {
		t.Errorf("LossPercent(1,4) = %v, want 25", got)
	}
	if got := LossPercent(0, 0); got != 0 {
		t.Errorf("LossPercent(0,0) = %v, want 0", got)
	}
	if got := LossPercent(9, 4); !almost(got, 100) {
		t.Errorf("LossPercent clamps to 100, got %v", got)
	}
}

// scriptedProber returns a fixed sequence of (rtt, err) pairs.
type scriptedProber struct {
	rtts []time.Duration
	errs []bool // true => return an error for that probe
	i    int
}

func (s *scriptedProber) Probe(_ context.Context, _ net.IP, _ string) (time.Duration, error) {
	i := s.i
	s.i++
	if i < len(s.errs) && s.errs[i] {
		return 0, errors.New("probe failed")
	}
	return s.rtts[i], nil
}

func TestMeasureWithScriptedProber(t *testing.T) {
	p := &scriptedProber{
		rtts: []time.Duration{20 * time.Millisecond, 30 * time.Millisecond, 0, 40 * time.Millisecond},
		errs: []bool{false, false, true, false},
	}
	m := Measure(context.Background(), nil, "eth0", Options{Target: "x:443", Count: 4}, p)

	if m.Samples != 4 || m.Success != 3 {
		t.Fatalf("samples/success = %d/%d, want 4/3", m.Samples, m.Success)
	}
	if !almost(m.LossPct, 25) {
		t.Errorf("LossPct = %v, want 25", m.LossPct)
	}
	if !almost(m.LatencyMs, 30) { // mean of 20,30,40
		t.Errorf("LatencyMs = %v, want 30", m.LatencyMs)
	}
	if !m.OK {
		t.Error("expected OK=true when at least one probe succeeds")
	}
	if m.Interface != "eth0" {
		t.Errorf("Interface = %q, want eth0", m.Interface)
	}
}

func TestMeasureAllFail(t *testing.T) {
	p := &scriptedProber{rtts: []time.Duration{0, 0}, errs: []bool{true, true}}
	m := Measure(context.Background(), nil, "wwan0", Options{Target: "x:443", Count: 2}, p)
	if m.OK {
		t.Error("expected OK=false when every probe fails")
	}
	if !almost(m.LossPct, 100) {
		t.Errorf("LossPct = %v, want 100", m.LossPct)
	}
}
