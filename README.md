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
- [ ] Reconciler core (desired vs live state, diff, apply plan)
- [ ] Deployment + Service rendering (image, replicas, resources, ports)
- [ ] Rollout strategies (recreate, rolling update, canary)
- [ ] Autoscaling (HPA from CPU / request-rate / GPU signals)
- [ ] Health + readiness gates (model-load probe, traffic shift)
- [ ] Metrics + events (Prometheus, Kubernetes events)
- [ ] CLI (`mlop`: apply, status, logs, rollout) + demo
- [ ] Self-review checklist, then public

## Quick start

```bash
go build ./...
go run ./cmd/operator --help
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
registry.

## License

MIT — see [LICENSE](LICENSE).
