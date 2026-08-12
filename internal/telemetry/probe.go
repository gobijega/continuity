package telemetry

import (
	"context"
	"net"
	"time"
)

// Prober measures a single round trip to target, optionally egressing a
// specific source address so the sample reflects one particular bearer.
type Prober interface {
	Probe(ctx context.Context, srcIP net.IP, target string) (time.Duration, error)
}

// TCPProber measures the time to complete a TCP handshake to target
// (host:port). It needs no elevated privileges, works on any platform, and —
// by binding LocalAddr — forces the connection out of a chosen interface.
// A future ICMPProber can slot in behind the same interface for finer loss
// measurement where raw sockets are permitted.
type TCPProber struct {
	Timeout time.Duration
}

// Probe dials target and returns the handshake round-trip time.
func (p TCPProber) Probe(ctx context.Context, srcIP net.IP, target string) (time.Duration, error) {
	d := net.Dialer{Timeout: p.Timeout}
	if srcIP != nil {
		d.LocalAddr = &net.TCPAddr{IP: srcIP}
	}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

// Options controls a measurement burst.
type Options struct {
	Target   string
	Count    int
	Interval time.Duration
	Timeout  time.Duration
}

// Metrics is the health record produced for one bearer against one target.
type Metrics struct {
	Interface      string    `json:"interface"`
	Target         string    `json:"target"`
	Samples        int       `json:"samples"`
	Success        int       `json:"success"`
	LatencyMs      float64   `json:"latency_ms"`
	JitterMs       float64   `json:"jitter_ms"`
	LossPct        float64   `json:"loss_pct"`
	ThroughputMbps float64   `json:"throughput_mbps"`
	OK             bool      `json:"ok"`
	At             time.Time `json:"at"`
}

// Measure runs a burst of probes for one interface/target and folds the
// results into a Metrics record. It is deterministic given a deterministic
// Prober, so callers can unit-test it without touching the network. Probing
// stops early if ctx is cancelled.
func Measure(ctx context.Context, srcIP net.IP, iface string, opt Options, prober Prober) Metrics {
	if opt.Count <= 0 {
		opt.Count = 1
	}
	rtts := make([]float64, 0, opt.Count)
	attempts, failed := 0, 0

	for i := 0; i < opt.Count; i++ {
		if i > 0 && opt.Interval > 0 {
			select {
			case <-ctx.Done():
				i = opt.Count // stop the burst
				continue
			case <-time.After(opt.Interval):
			}
		}
		if ctx.Err() != nil {
			break
		}
		attempts++
		d, err := prober.Probe(ctx, srcIP, opt.Target)
		if err != nil {
			failed++
			continue
		}
		rtts = append(rtts, float64(d.Microseconds())/1000.0)
	}

	m := Metrics{
		Interface: iface,
		Target:    opt.Target,
		Samples:   attempts,
		Success:   len(rtts),
		LossPct:   LossPercent(failed, attempts),
		At:        time.Now(),
	}
	if len(rtts) > 0 {
		m.LatencyMs = round2(Mean(rtts))
		m.JitterMs = round2(Jitter(rtts))
		m.OK = true
	}
	return m
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// MeasureBest probes each target in turn and keeps the healthiest result,
// stopping early once a lossless path is found. It is shared by the CLI scan
// and the live agent source.
func MeasureBest(ctx context.Context, srcIP net.IP, iface string, targets []string, opt Options, prober Prober) Metrics {
	var best Metrics
	first := true
	for _, tgt := range targets {
		o := opt
		o.Target = tgt
		m := Measure(ctx, srcIP, iface, o, prober)
		if first || Better(m, best) {
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

// Better reports whether metrics a are healthier than b: available beats
// unavailable, then lower loss, then lower latency.
func Better(a, b Metrics) bool {
	if a.OK != b.OK {
		return a.OK
	}
	if a.LossPct != b.LossPct {
		return a.LossPct < b.LossPct
	}
	return a.LatencyMs < b.LatencyMs
}
