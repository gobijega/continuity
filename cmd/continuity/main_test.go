package main

import (
	"strings"
	"testing"

	"github.com/gobijega/continuity/internal/interfaces"
	"github.com/gobijega/continuity/internal/telemetry"
)

func TestSplitTargets(t *testing.T) {
	got := splitTargets(" 1.1.1.1:443 , ,8.8.8.8:443,")
	if len(got) != 2 || got[0] != "1.1.1.1:443" || got[1] != "8.8.8.8:443" {
		t.Errorf("splitTargets = %v, want [1.1.1.1:443 8.8.8.8:443]", got)
	}
}

func TestDash(t *testing.T) {
	if dash("") != "-" {
		t.Error("dash(empty) should be -")
	}
	if dash("x") != "x" {
		t.Error("dash(non-empty) should pass through")
	}
}

func TestBar(t *testing.T) {
	if n := strings.Count(bar(100), "█"); n != 10 {
		t.Errorf("bar(100) filled = %d, want 10", n)
	}
	if n := strings.Count(bar(0), "█"); n != 0 {
		t.Errorf("bar(0) filled = %d, want 0", n)
	}
	if !strings.Contains(bar(92.5), "92.5") {
		t.Error("bar should include the numeric score")
	}
}

func TestCostScore(t *testing.T) {
	if costScore(interfaces.KindCellular) >= costScore(interfaces.KindEthernet) {
		t.Error("cellular (metered) should score lower on cost than ethernet")
	}
}

func TestBetterMetrics(t *testing.T) {
	up := telemetry.Metrics{OK: true, LossPct: 5, LatencyMs: 100}
	down := telemetry.Metrics{OK: false}
	if !betterMetrics(up, down) {
		t.Error("an OK metric should beat a not-OK one")
	}
	lowLoss := telemetry.Metrics{OK: true, LossPct: 1, LatencyMs: 200}
	if !betterMetrics(lowLoss, up) {
		t.Error("lower loss should win")
	}
	lowLat := telemetry.Metrics{OK: true, LossPct: 5, LatencyMs: 50}
	if !betterMetrics(lowLat, up) {
		t.Error("with equal loss, lower latency should win")
	}
}
