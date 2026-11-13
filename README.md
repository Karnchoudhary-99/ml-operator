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
- [x] Rollout strategies (recreate, rolling update, canary)
- [ ] Autoscaling (HPA from CPU / request-rate / GPU signals)
- [ ] Health + readiness gates (model-load probe, traffic shift) — canary health gate done; model-load probe remains
- [ ] Metrics + events (Prometheus, Kubernetes events)
- [ ] CLI (`mlop`: apply, status, logs, rollout) + demo
- [ ] Self-review checklist, then public

## Quick start

```bash
go build ./...
go run ./cmd/operator --help
# Watch the reconciler work without a cluster:
go run ./cmd/operator --demo
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
- `internal/api` — `ModelDeployment` types, validation, defaults
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
  all canary pods ready), max error rate, max p95 latency (a 0
  threshold disables that check). Zero ready replicas is *pending*,
  not a failure — a fresh canary must not burn its failure budget
  while its pods come up. Error rate and latency need a metrics
  source, so the gate is readiness-only until the metrics phase.
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
serving the previous version). Rollout state is per-deployment and
resets on a new generation or canary-spec change; a done state is
kept so the same generation never restarts. `operator --demo` runs
a full canary rollout (10% → 55% → 100% → promote) against the
fake cluster.

## License

MIT — see [LICENSE](LICENSE).
