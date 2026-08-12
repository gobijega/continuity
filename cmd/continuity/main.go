// Command continuity is the Continuity Edge Agent (Sprint 1–3 demonstrator).
//
// Each scan it discovers the host's network interfaces, measures live latency,
// jitter and packet loss on every usable bearer, computes a 0–100 health score
// under the selected application profile, and prints a ranked table naming the
// PRIMARY and BACKUP paths. This is the sensing-and-scoring core that the
// policy engine, route failover and dashboard build on in later sprints.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gobijega/continuity/internal/config"
	"github.com/gobijega/continuity/internal/interfaces"
	"github.com/gobijega/continuity/internal/scoring"
	"github.com/gobijega/continuity/internal/telemetry"
)

// Version is overridable at build time via -ldflags "-X main.Version=...".
var Version = "0.1.0-dev"

type row struct {
	Interface string            `json:"interface"`
	Kind      string            `json:"kind"`
	Address   string            `json:"address"`
	Metrics   telemetry.Metrics `json:"metrics"`
	Score     scoring.Score     `json:"score"`
	Role      string            `json:"role"`
}

type report struct {
	Node    string    `json:"node"`
	Profile string    `json:"profile"`
	At      time.Time `json:"at"`
	Rows    []row     `json:"interfaces"`
}

func main() {
	var (
		once     = flag.Bool("once", false, "run a single scan and exit")
		jsonOut  = flag.Bool("json", false, "emit JSON instead of a table")
		interval = flag.Duration("interval", 2*time.Second, "delay between scans in continuous mode")
		probeInt = flag.Duration("probe-interval", 0, "delay between probes within a bearer (overrides config)")
		profile  = flag.String("profile", "", "scoring profile: default|voice|video|telemetry|bulk")
		cfgPath  = flag.String("config", "", "path to a YAML config file")
		count    = flag.Int("count", 0, "probes per bearer per scan (overrides config)")
		targets  = flag.String("targets", "", "comma-separated host:port probe targets (overrides config)")
		showAll  = flag.Bool("all", false, "include loopback, virtual and down interfaces")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("continuity", Version)
		return
	}

	cfg := config.Default()
	if *cfgPath != "" {
		c, err := config.Load(*cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "continuity: config: %v\n", err)
			os.Exit(1)
		}
		cfg = c
	}
	if *profile != "" {
		cfg.Profile = *profile
	}
	if *count > 0 {
		cfg.Probe.Count = *count
	}
	if *probeInt > 0 {
		cfg.Probe.Interval = *probeInt
	}
	if *targets != "" {
		cfg.Probe.Targets = splitTargets(*targets)
	}

	weights := scoring.ProfileWeights(cfg.Profile)
	thresh := scoring.DefaultThresholds()
	prober := telemetry.TCPProber{Timeout: cfg.Probe.Timeout}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run := func() {
		rep := scan(ctx, cfg, weights, thresh, prober, *showAll)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
		} else {
			renderTable(os.Stdout, rep)
		}
	}

	run()
	if *once {
		return
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func scan(ctx context.Context, cfg config.Config, w scoring.Weights, t scoring.Thresholds, prober telemetry.Prober, all bool) report {
	rep := report{Node: cfg.Node, Profile: cfg.Profile, At: time.Now()}
	ifs, err := interfaces.Discover()
	if err != nil {
		fmt.Fprintf(os.Stderr, "continuity: discover: %v\n", err)
		return rep
	}
	for _, it := range ifs {
		if !all && !it.Usable() {
			continue
		}
		src := net.ParseIP(it.PrimaryIPv4())
		m := measureBest(ctx, src, it.Name, cfg.Probe, prober)
		if mbps, ok := telemetry.LinkSpeedMbps(it.Name); ok {
			m.ThroughputMbps = mbps
		}
		in := scoring.Input{
			Available:      m.OK,
			LatencyMs:      m.LatencyMs,
			JitterMs:       m.JitterMs,
			LossPct:        m.LossPct,
			ThroughputMbps: m.ThroughputMbps,
			ReliabilityPct: 100, // no historical record yet (future sprint)
			CostScore:      costScore(it.Kind),
		}
		rep.Rows = append(rep.Rows, row{
			Interface: it.Name,
			Kind:      string(it.Kind),
			Address:   it.PrimaryIPv4(),
			Metrics:   m,
			Score:     scoring.Compute(in, w, t),
		})
	}

	sort.SliceStable(rep.Rows, func(a, b int) bool {
		return rep.Rows[a].Score.Total > rep.Rows[b].Score.Total
	})
	for i := range rep.Rows {
		switch {
		case rep.Rows[i].Score.Total <= 0:
			rep.Rows[i].Role = "down"
		case i == 0:
			rep.Rows[i].Role = "primary"
		case i == 1:
			rep.Rows[i].Role = "backup"
		default:
			rep.Rows[i].Role = "standby"
		}
	}
	return rep
}

// measureBest probes each configured target and keeps the healthiest result,
// stopping early once a lossless path is found.
func measureBest(ctx context.Context, src net.IP, iface string, pc config.Probe, prober telemetry.Prober) telemetry.Metrics {
	var best telemetry.Metrics
	first := true
	for _, tgt := range pc.Targets {
		m := telemetry.Measure(ctx, src, iface, telemetry.Options{
			Target:   tgt,
			Count:    pc.Count,
			Interval: pc.Interval,
			Timeout:  pc.Timeout,
		}, prober)
		if first || betterMetrics(m, best) {
			best, first = m, false
		}
		if best.OK && best.LossPct == 0 {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	return best
}

func betterMetrics(a, b telemetry.Metrics) bool {
	if a.OK != b.OK {
		return a.OK
	}
	if a.LossPct != b.LossPct {
		return a.LossPct < b.LossPct
	}
	return a.LatencyMs < b.LatencyMs
}

// costScore reflects that metered bearers (cellular) are less desirable for
// cost-sensitive traffic. Later sprints will source this from policy.
func costScore(k interfaces.Kind) float64 {
	if k == interfaces.KindCellular {
		return 40
	}
	return 100
}

func splitTargets(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func renderTable(w io.Writer, rep report) {
	fmt.Fprintf(w, "\nJEGASEC Continuity  ·  node %s  ·  profile %s  ·  %s\n\n",
		rep.Node, rep.Profile, rep.At.Format("15:04:05"))
	if len(rep.Rows) == 0 {
		fmt.Fprintln(w, "  no bearers matched (try --all)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  BEARER\tTYPE\tADDRESS\tLAT(ms)\tJIT(ms)\tLOSS\tROLE\tSCORE\t")
	fmt.Fprintln(tw, "  ------\t----\t-------\t-------\t-------\t----\t----\t-----\t")
	for _, r := range rep.Rows {
		lat, jit := "-", "-"
		if r.Metrics.OK {
			lat = fmt.Sprintf("%.0f", r.Metrics.LatencyMs)
			jit = fmt.Sprintf("%.0f", r.Metrics.JitterMs)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t%s\t\n",
			r.Interface, r.Kind, dash(r.Address), lat, jit, r.Metrics.LossPct,
			strings.ToUpper(r.Role), bar(r.Score.Total))
	}
	tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func bar(score float64) string {
	const n = 10
	filled := int(score/100*n + 0.5)
	if filled > n {
		filled = n
	}
	if filled < 0 {
		filled = 0
	}
	return fmt.Sprintf("%5.1f  %s%s", score, strings.Repeat("█", filled), strings.Repeat("·", n-filled))
}
