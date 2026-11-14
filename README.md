# ML Model Operator

A Kubernetes operator for deploying ML models — declare a
`ModelDeployment` and the operator provisions the serving Deployment,
Service, autoscaling and rollout, then keeps the live state converged
with the desired state.

> **Status:** in progress (project #38 of the portfolio program).
> The operator core is dependency-free Go; the Kubernetes client
> integration lands with the controller-runtime phase of the roadmap.

## Roadmap

- [x] Project scaffold (README roadmap, license, gitignore, CI)
- [x] ModelDeployment CRD types + validation (spec, status, defaults)
- [x] In-memory model registry (register, list, get, delete, watch)
- [x] Reconciler core (desired vs live state, diff, apply plan)
- [x] Deployment + Service rendering (image, replicas, resources, ports)
- [x] Rollout strategies (recreate, rolling update, canary, blue-green)
- [x] Blue-green rollout (green soaks at full size, Service cutover, fold)
- [x] Autoscaling (HPA from CPU / request-rate / GPU signals)
- [x] Health + readiness gates (model-load probe, traffic shift)
- [x] Per-rollout health-gate thresholds (`canary.gate`)
- [x] Rollback to previous version (`mlop rollback`)
- [x] Diff preview + JSON output (`mlop diff`, `status`/`logs --json`)
- [x] Metrics + events (Prometheus, Kubernetes events)
- [x] Metrics HTTP endpoint (`--metrics-addr`, `/metrics` + `/healthz`)
- [x] Deployment history audit trail (`mlop history`)
- [x] CLI (`mlop`: apply, diff, status, logs, rollout) + demo
- [ ] Self-review checklist, then public

## Quick start

```bash
go build ./...
go run ./cmd/operator --help
# Watch the reconciler work without a cluster:
go run ./cmd/operator --demo
```

## The mlop CLI

`mlop` is the operator's command-line client. There is no real
cluster yet, so it works over the same in-memory world the demo
uses — registry, reconciler, fake cluster — persisted to a JSON
state file between invocations (default `~/.mlop/state.json`, or
`--state` / `$MLOP_STATE`). Separate invocations share one world:
apply in one process, status in the next.

```bash
go build -o mlop ./cmd/mlop

# Validate a manifest without changing anything:
mlop apply -f examples/iris-mlp.yaml --dry-run

# Apply it: registers the deployment (generation 1) and reconciles once.
mlop apply -f examples/iris-mlp.yaml
# applied default/iris-mlp (generation 1): phase=Progressing
#   - CreateDeployment default/iris-mlp (deployment does not exist)
#   - CreateService default/iris-mlp (service does not exist)

# Re-applying the same manifest is a no-op; a changed spec bumps
# the generation and rolls the model out.
mlop apply -f examples/iris-mlp.yaml
# unchanged default/iris-mlp (generation 1): phase=Progressing

# Status: a table for all deployments, or a detail view for one.
mlop status
mlop status iris-mlp
```

Before applying a changed manifest, preview exactly what the
reconciler would do — `mlop diff` computes the apply plan without
changing any state (no cluster writes, no status writes, no state
file write), so it is safe to run against a live deployment:

```bash
mlop diff iris-mlp
# in sync: default/iris-mlp (generation 1) — live state matches desired state

mlop diff -f examples/iris-mlp-canary.yaml
# diff default/iris-mlp (generation 2):
#   - UpdateDeployment default/iris-mlp (image "…/iris-mlp:v1" -> "…/iris-mlp:v2")
#   - CreateDeployment default/iris-mlp-canary (canary deployment does not exist)
```

For scripts and dashboards, `status`, `logs` and `diff` all take
`--json` and print the same data as JSON (an empty list is `[]`):

```bash
mlop status --json
mlop logs iris-mlp --json --tail 5
mlop diff -f examples/iris-mlp-canary.yaml --json
```

Manifests are YAML (the shape in `examples/iris-mlp.yaml`) or JSON
with the same keys. The parser is a small subset — nested maps,
string/int/bool scalars, `#` comments — with no external
dependency; unknown keys, lists, tabs and flow collections are
rejected with a line number. `mlop` assigns generations itself:
a new key starts at 1, a changed spec bumps it, an identical
re-apply keeps it. The autoscaler's scale-down stabilization
window is in-memory only, so it restarts on every invocation —
documented in `internal/store`.

The fake cluster has no pods, so nothing becomes ready on its
own. `mlop ready` simulates pods passing the readiness gate:

```bash
mlop ready iris-mlp --replicas 2
# default/iris-mlp: 2 pod(s) ready — phase=Available (2/2 ready)
```

Ready pods are not enough for a canary: the model itself has to
load into the serving container (Triton's
`/v2/models/<name>/ready`). `mlop load` simulates that probe —
loaded by default, `--loading` while the model warms up,
`--fail` when it fails to load — and `--canary` targets the
canary Deployment:

```bash
mlop load iris-mlp --canary --loading   # gate holds, no failure counted
mlop load iris-mlp --canary             # model loaded: the gate judges traffic signals
mlop load iris-mlp --canary --fail      # failed load: a real gate failure
```

A loading model holds the rollout at its weight without burning
failure budget; a failed load fails the gate, and three
consecutive failures abort the rollout. `examples/model-load-tour.sh`
drives both stories end to end (hold-then-promote, fail-then-abort).

`mlop logs` shows the recorded events for a deployment (newest
first; only notable transitions are recorded — promotions, scale
decisions, aborts, invalid specs — not routine reconciles), and
`mlop rollout status` reports a canary in flight:

```bash
mlop logs iris-mlp
mlop rollout status iris-mlp
# rollout: default/iris-mlp
#   model:      registry.example.com/iris-mlp:v2 (v2)
#   ladder:     [10 100]
#   step:       1/2 (weight 10%)
#   failures:   0 consecutive gate failures
#   state:      in progress (phase Progressing)
```

`mlop history` is the audit trail behind the events: one line per
generation / phase / image transition, oldest first, so it reads
like a timeline of what happened to the model. Steady-state
reconciles record nothing — only real transitions appear — and the
trail is capped at 50 entries per deployment, so the state file
never grows without bound:

```bash
mlop history iris-mlp
# 2026-11-14 20:15:03 gen 1 registry.example.com/iris-mlp:v1:v1 Available   2/2 replicas ready
mlop history iris-mlp --json   # same trail, machine-readable
```

`examples/history-tour.sh` drives the trail end to end: v1,
ready, canary v2, promote, rollback to v1 as generation 3.

A blue-green rollout (`strategy: BlueGreen`,
`examples/iris-mlp-bluegreen.yaml`) never splits traffic: v2
soaks as a full-size green Deployment beside stable v1, the
Service flips to green once the same health gate passes, and the
next reconcile folds green into stable. `mlop ready` and `mlop
load` take a `--green` flag to drive the green Deployment's
readiness and model-load probe, and `mlop rollout status`
reports soaking / switched / folded / aborted.
`examples/bluegreen-tour.sh` drives both stories end to end
(hold-then-switch-and-fold, fail-then-abort).

`examples/mlop-tour.sh` drives the whole flow end to end —
apply v1, ready, canary v2 (10% → 100% → promote), then asserts
v2 is live and the promotion is in the log:

```bash
examples/mlop-tour.sh
# tour passed: v1 applied, v2 canaried and promoted, events logged.
```

## How it works

1. You write a `ModelDeployment` manifest: model image, replicas,
   resources, traffic policy.
2. The operator validates it, registers the model, and computes the
   desired serving state (Deployment + Service + autoscaling).
3. The reconciler diffs desired vs live state and applies only what
   changed, recording every action as an event.
4. Rollouts are safe by default: a new model version is health-gated
   before traffic shifts to it.

## Layout

- `cmd/operator` — operator entrypoint (flags, graceful shutdown)
- `cmd/mlop` — CLI client (`apply`, `diff`, `status`, `logs`, `rollout`, `ready`, `load`, `rollback`)
- `internal/api` — `ModelDeployment` types, validation, defaults
- `internal/manifest` — YAML/JSON manifest parsing (no external deps)
- `internal/store` — JSON state file shared between mlop invocations
- `internal/registry` — in-memory model registry + watch
- `internal/controller` — reconciler (diff, apply plan, rollout)
- `internal/render` — Deployment / Service / HPA rendering
- `internal/rollout` — canary weight ladder, health gate, rollout state machine
- `internal/metrics` — Prometheus metrics, events

## API reference

A minimal manifest — everything else gets a default:

```yaml
name: iris-mlp
namespace: default
spec:
  model:
    image: registry.example.com/iris-mlp:v2
    framework: onnx
    version: v2
```

`ApplyDefaults` fills in: 1 replica, port 8080, `RollingUpdate`
strategy, CPU `500m`/`1`, memory `512Mi`/`1Gi`, readiness on
`/healthz` (or `/v2/health/ready` for Triton), 5s initial delay,
10s probe period. `Validate` is strict and returns every problem at
once: DNS-1123 names, image required, port 1–65535, resource
quantities (`500m`, `2`, `512Mi`, `1Gi`, `1G`) with limit ≥ request,
strategy one of `Recreate` / `RollingUpdate` / `Canary` (canary needs
`weight` 1–99), autoscaling `min ≤ max` with at least one target, env
keys matching `[A-Za-z_][A-Za-z0-9_]*`, health path starting with `/`.

The registry (`internal/registry`) is thread-safe and revisioned:
`Register` upserts (a re-registration with identical spec and
generation is a true no-op — no revision bump, no event), `Get` /
`List` (sorted by `namespace/name`) / `Delete`, and `Watch` streams
`Added` / `Modified` / `Deleted` events to every subscriber. A slow
watcher drops its oldest buffered event instead of blocking the
registry. Status is written separately with `UpdateStatus`: it bumps
the global revision and emits a `Modified` event without touching
the spec or generation, because `Register` deliberately drops
status-only re-registrations.

The renderer (`internal/render`) turns a `ModelDeployment` into
plain-Go `Deployment` / `Service` values (no client-go): stable
selector labels (the version label lives on pod templates only, so a
version bump never changes the selector), deterministic sorted env,
image / resources / port / readiness probe from the spec, and a
ClusterIP Service on port 80 forwarding to the container port.
`DeploymentDiffReasons` / `ServiceDiffReasons` name exactly which
managed fields drifted, so the reconciler applies only what changed.

The reconciler (`internal/controller`) is level-triggered and
idempotent: it defaults and validates the spec (an invalid spec
becomes `Failed` and never reaches the cluster), renders the desired
state, diffs it against the live cluster, and applies the minimal
plan — create, update, or a single `NoOp` when already in sync.
`Recreate` image changes delete-then-create so versions never serve
side by side. Status moves `Pending` → `Progressing` → `Available`
(`Degraded` while serving below the replica count), with `Ready` /
`Progressing` / `Available` conditions rebuilt every reconcile; a
deployment that leaves the registry has its Deployment, canary and
Service garbage-collected. The `Cluster` interface is the only
cluster surface the reconciler needs, and `FakeCluster` implements
it in memory for tests and the demo loop (new Deployments start at
0 ready replicas until `SetReady` simulates pods passing the gate).

### Canary rollouts

`Canary` keeps the stable Deployment on the previous version while
a small canary Deployment (`<name>-canary`) serves the new one. The
traffic model is pod-count based: the Service selector matches both
stable and canary pods, so traffic splits by ready-pod count, and
the weight ladder sizes the canary. Stable pods carry
`ml-operator/canary=false` and canary pods `=true`, and each
Deployment's selector requires its own value, so neither Deployment
adopts the other's pods.

`internal/rollout` owns the decisions, pure and unit-tested:

- `StepWeights` builds the ladder from the spec — initial weight,
  even increments, last step always exactly 100% (e.g. weight 10,
  3 steps → `[10 40 70 100]`; 0 steps means one jump to 100%).
- `Evaluate` checks the health gate: minimum ready ratio (default
  all canary pods ready), max error rate, max p95 latency — a 0
  threshold disables that check at this layer. In practice the
  reconciler never passes a 0: each rollout carries its own
  thresholds from a `canary.gate` block in the manifest
  (`maxErrorRate`, `maxP95LatencyMs`, `minReadyRatio`), and
  `ApplyDefaults` fills any zero or missing field with the
  operator defaults (5% errors, 500ms p95, all pods ready), so a
  threshold tunes the gate but never disables its check. The
  error-rate and latency samples come from the cluster metrics
  source when it reports them, and are skipped otherwise. Zero
  ready replicas is *pending*, not a failure — a fresh canary must
  not burn its failure budget while its pods come up. The
  model-load probe is pending the same way: when the cluster
  reports the model has not finished loading yet, the gate holds
  until it has, whatever pod readiness says. When the probe
  reports the model *failed* to load (bad artifact, OOM, missing
  weights), that is a real failure, not a hold — it burns failure
  budget, so a broken model aborts the rollout instead of waiting
  forever for a load that will never finish.
- The state machine holds, advances (once the step interval has
  elapsed), promotes at the final weight, or aborts after 3
  consecutive gate failures (configurable). A pass clears the
  failure count, so one bad sample holds the rollout instead of
  killing it.

The reconciler drives the machine once per reconcile: it pins the
stable Deployment to the live image until promotion, renders the
canary at the current ladder weight, and acts on the decision —
advance re-renders the canary at the next weight immediately,
promote folds the new image into the stable Deployment and deletes
the canary, abort deletes the canary and reports `Degraded` with no
requeue (terminal for that generation; the stable Deployment keeps
serving the previous version, and later reconciles keep the
deployment `Degraded` for that generation instead of flipping it
back to `Available` on readiness alone). Rollout state is per-deployment and
resets on a new generation or canary-spec change; a done state is
kept so the same generation never restarts. `operator --demo` runs
a full canary rollout (10% → 55% → 100% → promote) against the
fake cluster.

### Rollback

The status remembers the image the stable Deployment served before
the current one (`status.previousImage` / `previousVersion`,
recorded by the reconciler on every image change — canary
promotion or direct rolling/recreate update). `mlop rollback
<name>` restores it as a new generation, so it rolls out through
the deployment's normal strategy: a canary rollback is itself
canary-gated, and the version being rolled back from becomes the
new previous image, so a second rollback returns to it. It refuses
when there is no previous version yet, or the deployment already
serves it. `mlop status <name>` shows the rollback target, and
`examples/rollback-tour.sh` walks the whole story end to end.

### Autoscaling

Enable it per deployment with `spec.autoscaling` (`minReplicas`,
`maxReplicas`, `targetCPUUtilization` and/or
`targetRequestsPerSecond` — at least one target required). The
operator renders a matching `HorizontalPodAutoscaler` and drives
the same decisions in-process, so the fake cluster and the tests
scale without a metrics server:

```yaml
spec:
  autoscaling:
    minReplicas: 1
    maxReplicas: 5
    targetCPUUtilization: 70
    targetRequestsPerSecond: 100
```

`internal/autoscale` is pure decision logic: each enabled target
proposes `ceil(current × observed / target)`, the largest proposal
wins, and the result is clamped to `[min, max]`. Scale-up applies
immediately; scale-down waits out a 5-minute stabilization window
(HPA default) after the last scale-down, so a brief dip never
churns pods. No metrics observation means hold — unknown load
never scales. While autoscaling is on, the scaler owns the replica
count: `spec.replicas` is only the starting point, the same
ownership a real HPA has. Disabling autoscaling garbage-collects
the leftover HPA. `operator --demo` runs a spike (140% CPU, 2 → 4
replicas) and a dip (10% CPU, 4 → 1) after the canary tour.

### Metrics + events

`internal/metrics` is the operator's observability surface,
dependency-free: a Prometheus-style collector (`Collector.Inc` /
`Set` / `Render`, exposition format) and a Kubernetes-style event
recorder (bounded ring, identical events aggregate with a count,
newest first). The reconciler records every reconcile outcome
(`ml_operator_reconciles_total{result}`), rollout promotions and
aborts (`ml_operator_rollouts_total{result}`), scaling decisions
(`ml_operator_scale_events_total{direction=up|down|held}`), apply
retries (`ml_operator_apply_retries_total{outcome=recovered|exhausted}`),
ready
and desired replica gauges per deployment, and events
(`ScaledUp`, `ScaledDown`, `ScaleDownHeld`, `CanaryPromoted`,
`RolloutAborted`, `InvalidSpec`, `ApplyFailed`, `ApplyRetried`). The demo prints
both at the end, and the operator serves the exposition live on
`--metrics-addr` (default `:8080`, `/metrics` plus a `/healthz`
liveness probe) — an empty exposition until the first reconcile
records a series is normal. The real Kubernetes event sink lands
with the controller-runtime phase.

## License

MIT — see [LICENSE](LICENSE).
