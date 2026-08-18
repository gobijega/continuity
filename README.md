# JEGASEC Continuity — Mission-Aware Edge Agent

> **Networks optimise for connectivity. Continuity optimises for the mission.**

Continuity is an edge-deployed communications orchestrator. It continuously
scores every available network bearer (cellular, satellite, radio, Wi-Fi and
wired) on live latency, jitter and packet loss, and autonomously moves traffic
to the best path the moment the active link degrades. Anti-flap hysteresis stops
it reacting to noise, and an encrypted overlay carries live sessions across each
failover so connections never drop. It runs on standard Linux at the edge, is
hardware-agnostic, has **zero external dependencies**, and needs no cloud
connectivity.

What sets Continuity apart is that it understands **why** communications matter
in the current operational context. A conventional selector ranks links on
network metrics alone. Continuity adds a **Mission Context Engine** — a mission
profile, an operational state and application/traffic priority — that re-weights
those metrics, so the *same* bearer conditions can produce *different*
communications decisions as the mission changes. The defining demonstration:
with 5G measuring objectively best, routine operations select 5G, but a
mission-critical state selects the more resilient SATCOM path — and the console
shows exactly why.

This repository is the **Continuity Edge Agent**, the complete **Continuity 0.1**
demonstrator (Sprints 1–10): interface discovery, link telemetry, scoring, the
policy engine with tuned hysteresis and anti-flap recovery, automated failover,
an encrypted stable-overlay session-continuity layer, a degradation simulator, a
scripted 90-second demonstration, and a live web console. A public demonstrator
runs at **[jscontinuity.systems](https://jscontinuity.systems)**, where you can
trigger adversarial scenarios (denial-of-service, a kinetic anti-satellite
strike, and RF jamming) and watch the agent re-select the highest-scored
surviving network in real time. It is a working demonstrator, not a fielded
system.

![Continuity dashboard](docs/dashboard.png)

*Live dashboard after a simulated 5G degradation: the agent has autonomously
failed over 5G → SATCOM and logged the decision, staying RESILIENT throughout.*

## Try the demo (no root, no radios)

Requires Go 1.24+.

```sh
go run ./cmd/continuity serve --demo   # auto-runs the scripted 90-second showcase
# open http://127.0.0.1:8080 and watch it fail over and recover on its own
```

`--demo` drives a repeatable ~90-second story — 5G congestion, failover to
SATCOM with session continuity, recovery and fail-back — while the agent reacts
autonomously and the dashboard narrates each beat. For hands-on control instead:

```sh
go run ./cmd/continuity serve --sim
# open http://127.0.0.1:8080  →  trigger an attack scenario and watch it fail over
```

The `--sim` flag runs the whole control loop over a built-in simulator. The
console is mission-aware: switch the **mission profile** and **mission state**,
or fire a one-click **mission scenario** (Routine Mobility, Coverage Loss,
Mission Priority Override, Critical Traffic Event, Contested Environment) and
watch the mission-weighted scoring, the network-only vs mission-aware comparison,
the traffic policy and the decision timeline update live. You can also launch the
adversarial scenarios (denial-of-service, a kinetic anti-satellite strike, and RF
jamming) and see real scoring, policy and failover decisions as the agent
re-selects the best surviving bearer, without touching the network.

## Or inspect real bearers

```sh
go run ./cmd/continuity --once            # single scan of this host, table output
go run ./cmd/continuity --profile voice   # continuous, voice-weighted scoring
go run ./cmd/continuity --json            # machine-readable
go run ./cmd/continuity serve             # live dashboard over real interfaces
```

```
JEGASEC Continuity  ·  node vehicle-01  ·  profile default

  BEARER  TYPE      ADDRESS       LAT(ms)  JIT(ms)  LOSS  ROLE     SCORE
  wwan0   cellular  10.12.4.8     28       4        0%    PRIMARY   88.6  █████████·
  sat0    tunnel    100.72.0.3    140      18       0%    BACKUP    84.1  ████████··
  wlan0   wifi      192.168.1.20  85       9        12%   STANDBY   47.4  ████······
```

## How it works

1. **Discover** every network interface and classify it by bearer type
   (`internal/interfaces`).
2. **Measure** live latency, jitter and packet loss per bearer — each probe
   source-bound so bearers are measured independently (`internal/telemetry`).
3. **Score** each bearer 0–100 under an explicit, weighted, explainable model
   with per-application profiles (`internal/scoring`).
4. **Decide** with hysteresis — rest on the preferred bearer, migrate only when
   another is clearly and persistently better, fail back on recovery, never flap
   (`internal/policy`).
5. **Act** by making the chosen bearer the active route (`internal/routing`;
   DryRun by default, real Linux routing with `--apply`).
6. **Preserve** application sessions across the switch: a stable, encrypted
   overlay rebinds onto the new bearer so TCP/app sessions never see the
   failover (`internal/tunnel`).
7. **Observe** through an event log, REST API and dashboard (`internal/events`,
   `internal/api`).

The `internal/agent` package is the control loop tying these together over a
pluggable live-or-simulated source.

## Mission awareness (`internal/mission`)

The **Mission Context Engine** turns a selected mission profile and operational
state into the concrete inputs the decision engine consumes:

- **Mission profiles** (simulated): `ROUTINE`, `MISSION_CRITICAL`, `EMERGENCY`,
  `CONTESTED` — each a different standing objective and network-component
  weighting.
- **Mission state** (separate from profile): `NORMAL` → `ELEVATED` → `DEGRADED`
  → `CRITICAL`, which sharpens the weighting toward reliability, availability and
  structural resilience as pressure rises.
- **Application / traffic classes** (Command & Control, Safety, Telemetry, Voice,
  Operational Data, Video/ISR, Bulk, Background Sync) with per-state policy
  outcomes — `PRIORITISE` / `NORMAL` / `THROTTLE` / `DEFER` / `SUSPEND`.
- **Mission-weighted bearer scoring**: each bearer gets a conventional
  **network score**, a **mission score** (metrics re-weighted by mission policy,
  plus a structural-resilience term the network-only view ignores), and a
  **mission suitability** multiplier per bearer type. The value the controller
  selects on is `effective = mission score × suitability`.
- **Network-only vs mission-aware comparison** with a mathematically-derived
  **mission-influence** metric (total-variation distance between the mission and
  network-only weight vectors, plus bearer-preference deviation).
- **Mission-modulated hysteresis**: mission state scales the switching threshold
  and dwell (raised in `NORMAL`, lowered in `CRITICAL`) — it augments the
  anti-flap machinery, never removes it.

Mission context is a genuine input to the decision engine, not a UI label.
Everything in `internal/mission` is **simulated policy** for a software
demonstrator: it models mission-aware bearer selection and does not encode
classified threat models, certified doctrine, or validated operational
suitability. The engine is modular, so a real external mission-context source
could replace the simulator behind the same `Context` interface.

## REST API (spec §22)

| Method & path | Purpose |
|---|---|
| `GET /api/v1/state` | Full snapshot (node, status, active path, bearers, events) |
| `GET /api/v1/node` | Node id, status, active interface |
| `GET /api/v1/interfaces` | All bearers with metrics + score + role |
| `GET /api/v1/interfaces/{name}` | One bearer |
| `GET /api/v1/events` | Recent decision / state-change events |
| `GET /api/v1/policy` | Active profile and traffic-class → profile map |
| `GET /api/v1/tunnel` | Session-continuity overlay: address, active endpoint, cipher, rebinds |
| `GET /api/v1/demo` | Scripted-demonstration narrative and progress (demo mode) |
| `GET /api/v1/decisions[?format=csv]` | Mission-aware decision log for replay / analysis (JSON or CSV) |
| `POST /api/v1/simulator/{name}/{degrade\|outage\|restore}` | Demo controls (sim mode) |
| `POST /api/v1/scenario/{dos\|asat\|jamming\|restore}` | Adversarial demo scenarios (sim/demo mode) |
| `POST /api/v1/mission/profile/{ROUTINE\|MISSION_CRITICAL\|EMERGENCY\|CONTESTED}` | Switch mission profile |
| `POST /api/v1/mission/state/{NORMAL\|ELEVATED\|DEGRADED\|CRITICAL}` | Switch operational mission state |
| `POST /api/v1/mission/scenario/{name}` | One-click mission scenario preset (sets profile + state + conditions) |

The `/api/v1/state` snapshot now also carries a `mission` block (profile, state,
active traffic class, mission vs network weights, per-class traffic policy,
network-only vs mission-aware pick, override flag, influence, decision reasons
and live hysteresis) and a `decisions` array (recent mission-aware decision
records).

## Project layout

```
continuity/
  cmd/continuity/        # CLI: scan (sense) and serve (dashboard + API)
  internal/
    interfaces/          # Sprint 1 — discovery & classification
    telemetry/           # Sprint 2 — latency / jitter / loss / throughput
    scoring/             # Sprint 3 — weighted, explainable 0–100 score (+ structural resilience)
    mission/             # Mission Context Engine — profiles, states, traffic policy, mission-weighted scoring, influence
    policy/              # Sprints 4 & 8 — traffic classes + tuned hysteresis controller
    events/              # Sprint 4 — thread-safe event log
    routing/             # Sprint 5 — DryRun / Linux route managers
    simulator/           # Sprint 6 — synthetic bearers + tc/netem builders
    api/                 # Sprint 7 — REST API + embedded dashboard
    tunnel/              # Sprint 9 — encrypted stable-overlay session continuity
    demo/                # Sprint 10 — scripted 90-second demonstration
    agent/               # control loop (live or simulated source)
    config/              # YAML-subset loader (node, profile, probe, policy, tunnel)
```

## Configuration

Built-in defaults work out of the box; an optional YAML file overrides them —
node, scoring profile, probe targets, the failover hysteresis (`policy:`) and
the session-continuity overlay (`tunnel:`); see
[`configs/continuity.example.yaml`](configs/continuity.example.yaml). The loader
parses a small YAML subset with no third-party dependency.

## Testing

```sh
make test      # go test ./... -race -cover
make vet
make fmt
make build     # -> bin/continuity
```

Every package is unit-tested, including the failover state machine and its
anti-flap tuning (`internal/policy`), the encrypted session-continuity handshake
and rebind (`internal/tunnel`), the scripted demonstration runner
(`internal/demo`), and an end-to-end failover-and-recovery run through the
orchestrator over the simulator (`internal/agent`).

## Roadmap

Sprints 1–10 — **Continuity 0.1 complete** ✓: sensing core (1–3), policy engine
and failover (4–5), simulator and dashboard (6–7), tuned hysteresis with
anti-flap recovery (8), encrypted stable-overlay session continuity (9), and the
scripted 90-second demonstration (10). From here the product spec carries the
roadmap through 0.5 (design-partner MVP) to 2.0 (predictive resilience).

## Status & notes

- Route changes default to **DryRun** (recorded, not applied). `serve --apply`
  performs real Linux routing and requires `CAP_NET_ADMIN` (root).
- `--sim` drives the pipeline from a simulator; the `tc/netem` builders in
  `internal/simulator` impair real interfaces on a physical test bench.
- Engineering targets (sub-3-second failover, etc.) are design goals, not yet
  independently validated. Throughput is link capacity from sysfs; active
  measurement is future work.

## License

Proprietary — © 2026 Jegasec. All rights reserved. See [LICENSE](LICENSE).
