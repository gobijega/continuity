// Command continuity is the Continuity Edge Agent.
//
//	continuity [scan]   discover, probe, score and rank bearers (one-shot or live table)
//	continuity serve    run the control loop and serve the dashboard + REST API
//
// scan is the Sprint 1–3 sensing core; serve adds the policy engine, failover,
// simulator and web dashboard (Sprints 4–7).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gobijega/continuity/internal/agent"
	"github.com/gobijega/continuity/internal/api"
	"github.com/gobijega/continuity/internal/config"
	"github.com/gobijega/continuity/internal/demo"
	"github.com/gobijega/continuity/internal/interfaces"
	"github.com/gobijega/continuity/internal/mission"
	"github.com/gobijega/continuity/internal/policy"
	"github.com/gobijega/continuity/internal/routing"
	"github.com/gobijega/continuity/internal/scoring"
	"github.com/gobijega/continuity/internal/simulator"
	"github.com/gobijega/continuity/internal/telemetry"
	"github.com/gobijega/continuity/internal/tunnel"
)

// Version is overridable at build time via -ldflags "-X main.Version=...".
var Version = "0.2.0-dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			serveMain(args[1:])
			return
		case "scan":
			args = args[1:]
		case "version", "--version", "-version":
			fmt.Println("continuity", Version)
			return
		}
	}
	scanMain(args)
}

// ---------------------------------------------------------------- scan mode

func scanMain(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	once := fs.Bool("once", false, "run a single scan and exit")
	jsonOut := fs.Bool("json", false, "emit JSON instead of a table")
	interval := fs.Duration("interval", 2*time.Second, "delay between scans in continuous mode")
	probeInt := fs.Duration("probe-interval", 0, "delay between probes within a bearer (overrides config)")
	profile := fs.String("profile", "", "scoring profile: default|voice|video|telemetry|bulk")
	cfgPath := fs.String("config", "", "path to a YAML config file")
	count := fs.Int("count", 0, "probes per bearer per scan (overrides config)")
	targets := fs.String("targets", "", "comma-separated host:port probe targets (overrides config)")
	showAll := fs.Bool("all", false, "include loopback, virtual and down interfaces")
	showVer := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)

	if *showVer {
		fmt.Println("continuity", Version)
		return
	}

	cfg := loadConfig(*cfgPath)
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
		rep := runScan(ctx, cfg, weights, thresh, prober, *showAll)
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

func runScan(ctx context.Context, cfg config.Config, w scoring.Weights, t scoring.Thresholds, prober telemetry.Prober, all bool) report {
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
		m := telemetry.MeasureBest(ctx, src, it.Name, cfg.Probe.Targets,
			telemetry.Options{Count: cfg.Probe.Count, Interval: cfg.Probe.Interval, Timeout: cfg.Probe.Timeout}, prober)
		if mbps, ok := telemetry.LinkSpeedMbps(it.Name); ok {
			m.ThroughputMbps = mbps
		}
		in := scoring.Input{
			Available:      m.OK,
			LatencyMs:      m.LatencyMs,
			JitterMs:       m.JitterMs,
			LossPct:        m.LossPct,
			ThroughputMbps: m.ThroughputMbps,
			ReliabilityPct: 100,
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

// ---------------------------------------------------------------- serve mode

func serveMain(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "dashboard/API listen address")
	useSim := fs.Bool("sim", false, "run with the built-in simulator instead of live interfaces")
	runDemo := fs.Bool("demo", false, "auto-run the scripted 90-second demonstration (implies --sim)")
	profile := fs.String("profile", "", "scoring profile")
	node := fs.String("node", "", "node name")
	interval := fs.Duration("interval", time.Second, "control-loop interval")
	apply := fs.Bool("apply", false, "apply real Linux route changes (needs root)")
	noTunnel := fs.Bool("no-tunnel", false, "disable the session-continuity overlay")
	cfgPath := fs.String("config", "", "path to a YAML config file (live mode)")
	_ = fs.Parse(args)

	if *runDemo {
		*useSim = true // the scripted demo drives the simulator
	}

	cfg := loadConfig(*cfgPath)
	if *node != "" {
		cfg.Node = *node
	}
	if *profile != "" {
		cfg.Profile = *profile
	}

	var src agent.Source
	var sim *simulator.Sim
	if *useSim {
		sim = simulator.NewDemo()
		src = agent.NewSimSource(sim, "5g")
		if cfg.Node == "edge-node" {
			cfg.Node = "vehicle-01"
		}
	} else {
		src = agent.NewLiveSource(cfg.Probe)
	}

	var router routing.Manager = routing.NewDryRun()
	if *apply {
		router = routing.NewLinux()
	}

	var tun tunnel.Manager = tunnel.Disabled{}
	if cfg.Tunnel.Enabled && !*noTunnel {
		tun = tunnel.NewDryRun(cfg.Tunnel.Overlay, cfg.Tunnel.Sessions)
	}

	hyst := hysteresisFromConfig(cfg)
	if *useSim && *cfgPath == "" {
		// Snappier demo tuning when no explicit policy file is supplied, so the
		// dashboard shows failover and fail-back within a short demonstration.
		hyst = policy.Hysteresis{
			MinImprovement: 12, FailureThreshold: 35, RecoveryThreshold: 60,
			MinDwell: 3 * time.Second, DegradationHold: 2, RecoveryHold: 2, FlapPenalty: 6 * time.Second,
		}
	}

	// The Mission Context Engine makes the running demonstrator mission-aware by
	// default (spec §2): the agent scores every bearer against mission policy,
	// and the API can switch mission profile/state and apply scenario presets.
	missionEng := mission.NewEngine(mission.Routine, mission.StateNormal)
	a := agent.New(agent.Options{Node: cfg.Node, Profile: cfg.Profile, Source: src, Router: router, Tunnel: tun, Hyst: hyst, Mission: missionEng})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.New(a, sim)
	srv.SetMission(missionEng)

	if *runDemo && sim != nil {
		runner := demo.New(sim)
		srv.SetDemo(runner)
		go runDemoLoop(ctx, a, runner, *interval)
	} else {
		go a.Run(ctx, *interval)
	}
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sh)
	}()

	overlayMsg := "off"
	if st := tun.State(); st.Enabled {
		overlayMsg = fmt.Sprintf("%s/%s", st.Overlay, st.Cipher)
	}
	fmt.Printf("continuity serve — node %s — http://%s  (sim=%v, demo=%v, apply=%v, overlay=%s)\n",
		cfg.Node, *addr, *useSim, *runDemo, *apply, overlayMsg)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "continuity: serve:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- shared

func loadConfig(path string) config.Config {
	cfg := config.Default()
	if path != "" {
		c, err := config.Load(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "continuity: config: %v\n", err)
			os.Exit(1)
		}
		cfg = c
	}
	return cfg
}

// runDemoLoop drives the scripted demonstration and the control loop on one
// clock (Sprint 10): each tick advances the demo — which shapes the simulator —
// then ticks the agent, so the dashboard shows the real control loop responding
// to the scripted conditions rather than any staged output.
func runDemoLoop(ctx context.Context, a *agent.Agent, r *demo.Runner, interval time.Duration) {
	now := time.Now()
	r.Start(now)
	a.Tick(now)
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tk.C:
			r.Advance(t)
			a.Tick(t)
		}
	}
}

// hysteresisFromConfig maps the config's plain-data policy block onto the
// controller's hysteresis parameters (Sprint 8: tunable failover behaviour).
func hysteresisFromConfig(cfg config.Config) policy.Hysteresis {
	p := cfg.Policy
	return policy.Hysteresis{
		MinImprovement:    p.MinImprovement,
		FailureThreshold:  p.FailureThreshold,
		RecoveryThreshold: p.RecoveryThreshold,
		MinDwell:          p.MinDwell,
		DegradationHold:   p.DegradationHold,
		RecoveryHold:      p.RecoveryHold,
		FlapPenalty:       p.FlapPenalty,
	}
}

// costScore reflects that metered bearers (cellular) are less desirable for
// cost-sensitive traffic. Later sprints source this from policy.
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
