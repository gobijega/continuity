package demo

import (
	"testing"
	"time"

	"github.com/gobijega/continuity/internal/simulator"
)

func TestScriptWellFormed(t *testing.T) {
	s := Script()
	if len(s) == 0 {
		t.Fatal("script is empty")
	}
	if s[0].At != 0 {
		t.Errorf("first beat at %v, want 0", s[0].At)
	}
	last := time.Duration(-1)
	for i, st := range s {
		if st.At < last {
			t.Errorf("beat %d at %v is out of order (prev %v)", i, st.At, last)
		}
		last = st.At
		if st.Caption == "" {
			t.Errorf("beat %d has no caption", i)
		}
	}
}

func TestRunnerAppliesBeatsAndLoops(t *testing.T) {
	sim := simulator.NewDemo()
	script := []Step{
		{At: 0, Caption: "start"},
		{At: 2 * time.Second, Caption: "degrade", Action: func(s *simulator.Sim) { s.Degrade("5g", 400, 50, 25, 0.1) }},
		{At: 4 * time.Second, Caption: "hold"}, // 5g remains degraded
	}
	r := NewWith(sim, script)
	T := time.Unix(1700000000, 0)

	r.Start(T)
	st := r.State()
	if !st.Running || st.Caption != "start" || st.Step != 1 {
		t.Fatalf("after Start: %+v", st)
	}
	if want := (4*time.Second + tail).Seconds(); st.Total != want {
		t.Errorf("total = %v, want %v", st.Total, want)
	}

	// 2s: the degrade beat fires.
	r.Advance(T.Add(2 * time.Second))
	if c := r.State().Caption; c != "degrade" {
		t.Fatalf("caption at 2s = %q, want degrade", c)
	}
	if sim.Sample("5g", T.Add(2*time.Second)).LossPct == 0 {
		t.Error("expected 5g to show loss after the degrade beat")
	}

	// 4s: the hold beat; 5g stays degraded.
	r.Advance(T.Add(4 * time.Second))
	if c := r.State().Caption; c != "hold" {
		t.Fatalf("caption at 4s = %q, want hold", c)
	}

	// Past the tail: the script loops, restoring the sim to baseline.
	r.Advance(T.Add(4*time.Second + tail))
	st = r.State()
	if st.Loops != 1 {
		t.Errorf("loops = %d, want 1", st.Loops)
	}
	if st.Caption != "start" {
		t.Errorf("caption after loop = %q, want start", st.Caption)
	}
	if sim.Sample("5g", T).LossPct != 0 {
		t.Error("expected 5g restored to baseline after the loop")
	}
}

func TestRunnerIdleUntilStarted(t *testing.T) {
	r := NewWith(simulator.NewDemo(), Script())
	// Advance before Start must be a no-op.
	r.Advance(time.Unix(1700000000, 0))
	if st := r.State(); st.Running || st.Caption != "" {
		t.Fatalf("runner should be idle before Start, got %+v", st)
	}
}
