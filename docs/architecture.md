# Architecture

How the ML Model Operator is put together, and where the real
Kubernetes integration will plug in. The operator is deliberately
dependency-free Go: every package below has no external imports
beyond the standard library, so the core logic is testable without
a cluster.

## Components

```
manifest ──► api ──► registry ──► controller ──► render ──► cluster
                │         │            │  ▲           │
                │         │            │  │           ▼
                │         └────────────┘  │      fake cluster
                │                    rollout/autoscale
                └────────── metrics/events ◄──────────┘
```

| Package | Owns |
| --- | --- |
| `internal/api` | `ModelDeployment` types, `ApplyDefaults`, `Validate` |
| `internal/registry` | Thread-safe in-memory registry, revisions, watch events |
| `internal/render` | Desired Deployment / Service / HPA, and diff reasons |
| `internal/rollout` | Canary weight ladder, health gate, rollout state machine |
| `internal/autoscale` | Replica proposals + scale-down stabilization window |
| `internal/controller` | Reconciler: plan, apply, gate, status, garbage collection |
| `internal/metrics` | Prometheus-style collector + Kubernetes-style event recorder |
| `internal/manifest` | YAML-subset/JSON manifest parsing (no external deps) |
| `internal/store` | JSON state file shared between `mlop` invocations |
| `cmd/operator` | Operator entrypoint (`--demo` drives the fake cluster) |
| `cmd/mlop` | CLI client (`apply`, `diff`, `status`, `logs`, …) |

## The reconcile loop

`Reconciler.Reconcile` is level-triggered and idempotent — it
recomputes the desired state from the registered spec and the live
cluster every time, and applies only the difference:

1. **Defaults + validation.** `ApplyDefaults` fills replicas, port,
   strategy, resources and the health path; `Validate` rejects the
   rest. An invalid spec never reaches the cluster — the deployment
   goes `Failed` with the validation errors in its status message.
2. **Rollout state.** A Canary strategy keeps per-generation rollout
   state (weight ladder, step, consecutive failures). Any other
   strategy drops it, so returning to Canary starts fresh. A done
   state (promoted or aborted) is kept, so reconciling the same
   generation never restarts the rollout.
3. **Autoscaling.** When enabled, the scaler decides the effective
   replica count from the latest cluster metrics (largest proposal
   wins, clamped to min/max, scale-down held for 5 minutes). The
   scaler owns the count — `spec.replicas` is only the starting
   point, HPA semantics.
4. **Render + plan.** `render` builds the desired Deployment,
   Service and HPA. During a canary the stable Deployment keeps
   serving the previous image until promotion (only the image and
   version label are pinned). `plan` diffs desired vs live and
   returns the minimal ordered actions: Deployment, canary,
   Service, HPA. Recreate deletes before creating, so old and new
   model versions never serve side by side.
5. **Apply.** Each action executes against the `Cluster`
   interface. A failed apply is `Degraded` with requeue, not
   `Failed` — the spec is fine, the cluster operation broke.
6. **Gate (canary only).** The health gate reads the live canary's
   readiness plus error rate, p95 latency and the model-load probe
   from cluster metrics, against the rollout's own thresholds
   (`canary.gate`, defaulted to 5% errors / 500ms p95 / all pods
   ready). The state machine holds, advances, promotes or aborts
   (3 consecutive failures). A still-loading model holds without
   burning failure budget; a failed load is a real failure.
7. **Status.** The phase is recomputed from readiness every time
   (`Available` at full replicas, `Degraded` below, `Progressing`
   while converging) and the conditions are rebuilt, so a stale
   condition can never linger. An aborted rollout stays `Degraded`
   for its generation — readiness alone must not flip it back to
   `Available` while the stable Deployment serves the old version.

`Reconciler.Plan` runs steps 1–4 only and returns the plan without
changing anything — no cluster writes, no status writes, no metrics,
no events, no scaler or rollout state. `mlop diff` is built on it.

## Rollout state machine

```
         ┌─ gate passes ─► advance ─┐
         │                          ▼
   hold ◄┤                 final weight + gate passes
         │                          │
         └─ 3 failures ─► abort    └─► promote
```

- **Hold:** gate failed, model still loading, or the step interval
  has not elapsed. The canary stays at its weight.
- **Advance:** gate passed and the interval elapsed — the canary
  steps to the next weight immediately, so the new size is live
  for the next gate evaluation.
- **Promote:** gate passed at 100% — the stable Deployment takes
  the new image, the canary is deleted, and the replaced image is
  recorded as the rollback target (`status.previousImage`).
- **Abort:** 3 consecutive gate failures — the canary is deleted,
  the stable Deployment keeps serving the previous version, and
  the deployment stays `Degraded` until a new generation arrives.

## Generations and rollback

The generation is the CLI's job, never the manifest's: a new key
starts at 1, a changed spec bumps it, an identical re-apply keeps
it. The reconciler records the replaced image on every successful
image change (canary promotion or direct rolling/recreate update),
so `mlop rollback` can restore it as a new generation — rolling
out through the deployment's normal strategy, never by editing
history. A second rollback returns to the version just left.

## The cluster boundary

Everything the reconciler touches goes through the narrow
`controller.Cluster` interface (Deployments, Services, HPAs,
metrics, readiness). The only implementation today is the
in-memory `FakeCluster` — new Deployments start at 0 ready until
`SetReady` is called, which is how `mlop ready` and the demo
simulate pods coming up. The controller-runtime phase replaces
this with a real client without touching the reconciler: the plan,
gate and state machine only ever see the interface.

## State and persistence

`mlop` persists the registry, the fake cluster, rollout state and
events to a JSON state file (`~/.mlop/state.json` by default) via
`internal/store`, atomically (temp file + rename). The autoscaler's
scale-down stabilization window is in-memory only and restarts on
every invocation — documented in `internal/store`. Deleting the
state file resets the simulated world; nothing in it is a source
of truth beyond the manifests.
