package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c := Default()
	if len(c.Probe.Targets) == 0 || c.Probe.Count <= 0 {
		t.Fatal("Default() must provide probe targets and a positive count")
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	yaml := `# example policy
node: vehicle-01
profile: voice

probe:
  targets:
    - 9.9.9.9:443
    - 1.0.0.1:443
  count: 3
  interval_ms: 500
  timeout_ms: 800
`
	dir := t.TempDir()
	path := filepath.Join(dir, "continuity.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.Node != "vehicle-01" {
		t.Errorf("Node = %q, want vehicle-01", c.Node)
	}
	if c.Profile != "voice" {
		t.Errorf("Profile = %q, want voice", c.Profile)
	}
	if len(c.Probe.Targets) != 2 {
		t.Fatalf("Targets = %v, want the 2 from the file (defaults must be replaced, not appended)", c.Probe.Targets)
	}
	if c.Probe.Targets[0] != "9.9.9.9:443" || c.Probe.Targets[1] != "1.0.0.1:443" {
		t.Errorf("Targets = %v, want [9.9.9.9:443 1.0.0.1:443]", c.Probe.Targets)
	}
	if c.Probe.Count != 3 {
		t.Errorf("Count = %d, want 3", c.Probe.Count)
	}
	if c.Probe.Interval != 500*time.Millisecond {
		t.Errorf("Interval = %v, want 500ms", c.Probe.Interval)
	}
	if c.Probe.Timeout != 800*time.Millisecond {
		t.Errorf("Timeout = %v, want 800ms", c.Probe.Timeout)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("expected an error loading a missing file")
	}
}

func TestLoadPolicySection(t *testing.T) {
	yaml := `node: vehicle-01

policy:
  min_improvement: 12
  failure_threshold: 35
  recovery_threshold: 60
  min_dwell_ms: 3000
  degradation_hold: 3
  recovery_hold: 4
  flap_penalty_ms: 8000
`
	dir := t.TempDir()
	path := filepath.Join(dir, "continuity.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.Policy.MinImprovement != 12 || c.Policy.FailureThreshold != 35 || c.Policy.RecoveryThreshold != 60 {
		t.Errorf("thresholds = %+v, want 12/35/60", c.Policy)
	}
	if c.Policy.MinDwell != 3*time.Second {
		t.Errorf("MinDwell = %v, want 3s", c.Policy.MinDwell)
	}
	if c.Policy.DegradationHold != 3 || c.Policy.RecoveryHold != 4 {
		t.Errorf("holds = %d/%d, want 3/4", c.Policy.DegradationHold, c.Policy.RecoveryHold)
	}
	if c.Policy.FlapPenalty != 8*time.Second {
		t.Errorf("FlapPenalty = %v, want 8s", c.Policy.FlapPenalty)
	}
}

func TestPolicyDefaultsWhenAbsent(t *testing.T) {
	// A file with no policy block must leave the built-in policy defaults intact.
	yaml := "node: vehicle-01\nprofile: voice\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "continuity.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	def := Default().Policy
	if c.Policy != def {
		t.Errorf("Policy = %+v, want defaults %+v", c.Policy, def)
	}
}
