package agent

import (
	"context"
	"net"
	"time"

	"github.com/gobijega/continuity/internal/config"
	"github.com/gobijega/continuity/internal/interfaces"
	"github.com/gobijega/continuity/internal/telemetry"
)

// LiveSource reads real bearer metrics: it discovers usable interfaces and
// probes each one (source-bound) against the configured targets.
type LiveSource struct {
	probe  config.Probe
	prober telemetry.Prober
}

// NewLiveSource builds a live source from a probe configuration.
func NewLiveSource(p config.Probe) *LiveSource {
	return &LiveSource{probe: p, prober: telemetry.TCPProber{Timeout: p.Timeout}}
}

// Read discovers interfaces and measures each usable bearer.
func (l *LiveSource) Read(now time.Time) []Reading {
	ifs, err := interfaces.Discover()
	if err != nil {
		return nil
	}
	budget := l.probe.Timeout*time.Duration(max(1, l.probe.Count)) + time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	var out []Reading
	for _, it := range ifs {
		if !it.Usable() {
			continue
		}
		src := net.ParseIP(it.PrimaryIPv4())
		m := telemetry.MeasureBest(ctx, src, it.Name, l.probe.Targets,
			telemetry.Options{Count: l.probe.Count, Interval: l.probe.Interval, Timeout: l.probe.Timeout}, l.prober)
		if mbps, ok := telemetry.LinkSpeedMbps(it.Name); ok {
			m.ThroughputMbps = mbps
		}
		out = append(out, Reading{Name: it.Name, Kind: string(it.Kind), Metrics: m})
	}
	return out
}
