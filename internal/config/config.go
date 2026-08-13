// Package config loads the Continuity Edge Agent's runtime configuration.
//
// The agent ships with working defaults; an optional YAML file overrides them
// (spec §16). To keep the core dependency-free and offline-buildable, Load
// parses a small, well-defined subset of YAML — indentation-based maps,
// scalars and "- " string lists — which covers the example policy. It is
// intentionally strict and simple, not a general YAML implementation; a full
// parser can be swapped in once richer policy documents are needed.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Probe controls how each bearer is measured.
type Probe struct {
	Targets  []string
	Count    int
	Interval time.Duration
	Timeout  time.Duration
}

// Policy holds the failover controller's hysteresis parameters (spec §9). It
// mirrors policy.Hysteresis as plain data so the config package stays a
// dependency-free leaf; the command layer maps it onto the controller.
type Policy struct {
	MinImprovement    float64       // candidate must beat the active bearer by this many points
	FailureThreshold  float64       // active below this is "urgent" (dwell/penalty waived)
	RecoveryThreshold float64       // preferred at/above this counts as recovered
	MinDwell          time.Duration // minimum time on a bearer before switching again
	DegradationHold   int           // consecutive degraded cycles before a soft migrate
	RecoveryHold      int           // consecutive recovered cycles before failing back home
	FlapPenalty       time.Duration // cooldown before a soft migration back onto a just-left bearer
}

// Tunnel configures the session-continuity overlay (spec §13, Sprint 9).
// Enabled turns the encrypted overlay on; Overlay is the stable address
// applications bind to; Sessions models how many flows ride it.
type Tunnel struct {
	Enabled  bool
	Overlay  string
	Sessions int
}

// Config is the agent's runtime configuration.
type Config struct {
	Node    string
	Profile string
	Probe   Probe
	Policy  Policy
	Tunnel  Tunnel
}

// Default returns the built-in configuration used when no file is supplied.
func Default() Config {
	return Config{
		Node:    "edge-node",
		Profile: "default",
		Probe: Probe{
			Targets:  []string{"1.1.1.1:443", "8.8.8.8:443"},
			Count:    5,
			Interval: time.Second,
			Timeout:  1500 * time.Millisecond,
		},
		Policy: Policy{
			MinImprovement:    15,
			FailureThreshold:  30,
			RecoveryThreshold: 50,
			MinDwell:          10 * time.Second,
			DegradationHold:   2,
			RecoveryHold:      2,
			FlapPenalty:       20 * time.Second,
		},
		Tunnel: Tunnel{
			Enabled:  true,
			Overlay:  "100.64.0.1",
			Sessions: 3,
		},
	}
}

// Load reads a YAML-subset config file, overlaying it onto Default().
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	var section string
	inTargets := false
	targetsReplaced := false

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if strings.HasPrefix(trimmed, "- ") {
			if section == "probe" && inTargets {
				if !targetsReplaced {
					cfg.Probe.Targets = nil
					targetsReplaced = true
				}
				cfg.Probe.Targets = append(cfg.Probe.Targets, unquote(trimmed[2:]))
			}
			continue
		}

		key, val, _ := strings.Cut(trimmed, ":")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		if indent == 0 {
			inTargets = false
			switch key {
			case "node":
				cfg.Node = unquote(val)
			case "profile":
				cfg.Profile = unquote(val)
			case "probe":
				section = "probe"
			case "policy":
				section = "policy"
			case "tunnel":
				section = "tunnel"
			default:
				section = "" // unknown top-level block: skip its children
			}
			continue
		}

		if section == "probe" {
			switch key {
			case "targets":
				inTargets = val == ""
			case "count":
				inTargets = false
				cfg.Probe.Count = atoiDefault(val, cfg.Probe.Count)
			case "interval_ms":
				inTargets = false
				cfg.Probe.Interval = msDefault(val, cfg.Probe.Interval)
			case "timeout_ms":
				inTargets = false
				cfg.Probe.Timeout = msDefault(val, cfg.Probe.Timeout)
			default:
				inTargets = false
			}
		}

		if section == "policy" {
			inTargets = false
			switch key {
			case "min_improvement":
				cfg.Policy.MinImprovement = floatDefault(val, cfg.Policy.MinImprovement)
			case "failure_threshold":
				cfg.Policy.FailureThreshold = floatDefault(val, cfg.Policy.FailureThreshold)
			case "recovery_threshold":
				cfg.Policy.RecoveryThreshold = floatDefault(val, cfg.Policy.RecoveryThreshold)
			case "min_dwell_ms":
				cfg.Policy.MinDwell = msDefault(val, cfg.Policy.MinDwell)
			case "degradation_hold":
				cfg.Policy.DegradationHold = atoiDefault(val, cfg.Policy.DegradationHold)
			case "recovery_hold":
				cfg.Policy.RecoveryHold = atoiDefault(val, cfg.Policy.RecoveryHold)
			case "flap_penalty_ms":
				cfg.Policy.FlapPenalty = msDefault(val, cfg.Policy.FlapPenalty)
			}
		}

		if section == "tunnel" {
			inTargets = false
			switch key {
			case "enabled":
				cfg.Tunnel.Enabled = boolDefault(val, cfg.Tunnel.Enabled)
			case "overlay":
				cfg.Tunnel.Overlay = unquote(val)
			case "sessions":
				cfg.Tunnel.Sessions = atoiDefault(val, cfg.Tunnel.Sessions)
			}
		}
	}

	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	if strings.TrimSpace(c.Node) == "" {
		c.Node = "edge-node"
	}
	if strings.TrimSpace(c.Profile) == "" {
		c.Profile = "default"
	}
	if c.Probe.Count <= 0 {
		c.Probe.Count = 5
	}
	if c.Probe.Interval <= 0 {
		c.Probe.Interval = time.Second
	}
	if c.Probe.Timeout <= 0 {
		c.Probe.Timeout = 1500 * time.Millisecond
	}
	if len(c.Probe.Targets) == 0 {
		c.Probe.Targets = []string{"1.1.1.1:443", "8.8.8.8:443"}
	}
	if strings.TrimSpace(c.Tunnel.Overlay) == "" {
		c.Tunnel.Overlay = "100.64.0.1"
	}
	if c.Tunnel.Sessions < 1 {
		c.Tunnel.Sessions = 1
	}
	c.normalizePolicy()
}

// normalizePolicy repairs only nonsensical (negative) hysteresis values, leaving
// deliberate zeros intact: RecoveryHold 0 means "fail back on the first healthy
// cycle" and FlapPenalty 0 disables the cooldown — both valid tunings.
func (c *Config) normalizePolicy() {
	d := Policy{
		MinImprovement:    15,
		FailureThreshold:  30,
		RecoveryThreshold: 50,
		MinDwell:          10 * time.Second,
		DegradationHold:   2,
		RecoveryHold:      2,
		FlapPenalty:       20 * time.Second,
	}
	if c.Policy.MinImprovement < 0 {
		c.Policy.MinImprovement = d.MinImprovement
	}
	if c.Policy.FailureThreshold < 0 {
		c.Policy.FailureThreshold = d.FailureThreshold
	}
	if c.Policy.RecoveryThreshold < 0 {
		c.Policy.RecoveryThreshold = d.RecoveryThreshold
	}
	if c.Policy.MinDwell < 0 {
		c.Policy.MinDwell = d.MinDwell
	}
	if c.Policy.DegradationHold < 0 {
		c.Policy.DegradationHold = d.DegradationHold
	}
	if c.Policy.RecoveryHold < 0 {
		c.Policy.RecoveryHold = d.RecoveryHold
	}
	if c.Policy.FlapPenalty < 0 {
		c.Policy.FlapPenalty = d.FlapPenalty
	}
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

func floatDefault(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return def
}

func boolDefault(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return def
}

func msDefault(s string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 0 {
		return time.Duration(v) * time.Millisecond
	}
	return def
}
