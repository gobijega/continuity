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

// Config is the agent's runtime configuration.
type Config struct {
	Node    string
	Profile string
	Probe   Probe
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

func msDefault(s string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 0 {
		return time.Duration(v) * time.Millisecond
	}
	return def
}
