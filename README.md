# JEGASEC Continuity — Edge Agent

> Keeps mission-critical systems connected when their networks degrade or fail.

Continuity is edge-deployed software that maintains application connectivity
across multiple communications bearers — cellular, satellite, radio, Wi-Fi and
wired — when individual links become congested, degraded, jammed or unavailable.

This repository contains the **Edge Agent**: the autonomous sensing-and-scoring
core that runs on every Continuity node. It is **Continuity 0.1 (demonstrator),
Sprints 1–3** — interface discovery, link telemetry, and the scoring engine.
It is hardware-agnostic, runs on standard Linux, has **zero external
dependencies**, and needs no cloud connectivity.

## What it does today

- **Discovers** the host's network interfaces and classifies each by bearer type
  (ethernet / wifi / cellular / tunnel / virtual), reading Linux sysfs for
  operational state and device type.
- **Measures** live latency, jitter and packet loss per bearer. Each probe is
  bound to that interface's source address, so every bearer is measured
  independently even when several are up at once.
- **Scores** each bearer 0–100 under an explicit, configurable weighting, with
  application **profiles** (voice / video / telemetry / bulk) that re-rank the
  same links for different traffic classes. An unavailable bearer scores 0.
- **Ranks** bearers and names the PRIMARY and BACKUP paths — as a live console
  table or as JSON for downstream tooling.

## Quickstart

Requires Go 1.24+.

```sh
go run ./cmd/continuity --once             # single scan, table output
go run ./cmd/continuity --profile voice    # continuous, voice-weighted scoring
go run ./cmd/continuity --json             # machine-readable output
go run ./cmd/continuity --all              # include loopback / virtual / down
```

Illustrative output on a multi-bearer edge node:

```
JEGASEC Continuity  ·  node vehicle-01  ·  profile default  ·  04:17:20

  BEARER  TYPE      ADDRESS       LAT(ms)  JIT(ms)  LOSS  ROLE     SCORE
  wwan0   cellular  10.12.4.8     28       4        0%    PRIMARY   88.6  █████████·
  sat0    tunnel    100.72.0.3    610      22       0%    BACKUP    73.9  ███████···
  wlan0   wifi      192.168.1.20  85       9        12%   STANDBY   37.4  ████······
```

When 5G degrades (rising loss and latency), its score falls below the backup and
the ranking flips — the signal the policy engine and route-failover layers act
on in the next sprints.

## How the score works

Each raw metric maps onto a 0–100 sub-score against explicit thresholds
(`internal/scoring`), and the total is a weighted, normalised sum. The weights
come from the selected application **profile**, so a low-latency / low-bandwidth
link wins for voice while a high-bandwidth / high-latency link wins for bulk
transfer — the same inputs, ranked differently by intent. Every sub-score is
reported (see `--json`), so a decision can always be explained rather than
taken on trust.

## Project layout

```
continuity/
  cmd/continuity/        # CLI: discover → probe → score → rank
  internal/
    interfaces/          # Sprint 1 — discovery & classification
    telemetry/           # Sprint 2 — latency / jitter / loss / throughput
    scoring/             # Sprint 3 — weighted, explainable 0–100 score
    config/              # YAML-subset policy loader
  configs/               # example policy
  .github/workflows/     # CI: build, vet, gofmt, race tests
```

This mirrors the module organisation in the product specification (§24); later
sprints add `policy/`, `routing/`, `transport/`, `events/`, `api/` and
`simulator/`.

## Configuration

The agent runs on built-in defaults; an optional YAML file overrides them:

```sh
go run ./cmd/continuity --config configs/continuity.example.yaml
```

See [`configs/continuity.example.yaml`](configs/continuity.example.yaml). The
loader parses a small, well-defined YAML subset (no third-party dependency); a
full parser can be dropped in when richer policy documents are needed.

## Testing

```sh
make test      # go test ./... -race -cover
make vet
make fmt       # gofmt
make build     # -> bin/continuity
```

## Roadmap

Sprints 1–3 (this repo) → policy engine (4) → automated Linux route failover (5)
→ network-degradation simulator (6) → real-time dashboard (7) → hysteresis &
recovery (8) → encrypted stable tunnel (9) → the 90-second demonstration (10).
That completes **Continuity 0.1**; the product spec carries the roadmap through
0.5 (design-partner MVP) to 2.0 (predictive resilience).

## Status & notes

- Engineering targets (sub-3-second failover, etc.) are design goals, not yet
  independently validated.
- Throughput is currently link capacity read from sysfs; passive/active
  measurement is future work (spec §7).
- Latency/loss are measured via TCP-handshake probing (unprivileged, portable);
  an ICMP prober can slot in behind the same interface where raw sockets are
  permitted.

## License

Proprietary — © 2026 Jegasec. All rights reserved. See [LICENSE](LICENSE).
(If you later want this to be source-available, Apache-2.0 is a common choice.)
