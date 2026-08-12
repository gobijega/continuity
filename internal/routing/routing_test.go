package routing

import (
	"errors"
	"testing"
)

func TestDryRunTracksActive(t *testing.T) {
	d := NewDryRun()
	if d.Active() != "" {
		t.Fatal("new DryRun should have no active interface")
	}
	if err := d.Activate("wwan0"); err != nil {
		t.Fatal(err)
	}
	if d.Active() != "wwan0" {
		t.Errorf("Active = %q, want wwan0", d.Active())
	}
}

func TestDefaultRouteArgs(t *testing.T) {
	got := DefaultRouteArgs("sat0")
	want := []string{"route", "replace", "default", "dev", "sat0"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestLinuxActivateUsesRunner(t *testing.T) {
	var gotArgs []string
	l := &Linux{run: func(args ...string) error { gotArgs = args; return nil }}
	if err := l.Activate("eth1"); err != nil {
		t.Fatal(err)
	}
	if l.Active() != "eth1" {
		t.Errorf("Active = %q, want eth1", l.Active())
	}
	if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != "eth1" {
		t.Errorf("runner args = %v, want to end with eth1", gotArgs)
	}
}

func TestLinuxActivateError(t *testing.T) {
	l := &Linux{run: func(args ...string) error { return errors.New("boom") }}
	if err := l.Activate("eth1"); err == nil {
		t.Error("expected the error to propagate")
	}
	if l.Active() == "eth1" {
		t.Error("active must not update when activation fails")
	}
}
