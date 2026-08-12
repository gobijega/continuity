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
