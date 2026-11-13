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
- [ ] ModelDeployment CRD types + validation (spec, status, defaults)
- [ ] In-memory model registry (register, list, get, delete, watch)
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

## License

MIT — see [LICENSE](LICENSE).
