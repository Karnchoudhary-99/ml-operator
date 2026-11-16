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
| `internal/render` | Desired Deployment / Service / HPA / PodDisruptionBudget, and diff reasons |
| `internal/rollout` | Canary weight ladder, blue-green state machine, health gate, rollout state |
| `internal/autoscale` | Replica proposals + scale-down stabilization window |
| `internal/controller` | Reconciler: plan, apply, gate, status, garbage collection |
| `internal/metrics` | Prometheus-style collector + Kubernetes-style event recorder |
| `internal/manifest` | YAML-subset/JSON manifest parsing (no external deps) |
| `internal/store` | JSON state file shared between `mlop` invocations |
| `cmd/operator` | Operator entrypoint (`--demo` drives the fake cluster) |
| `cmd/mlop` | CLI client (`apply`, `diff`, `status`, `logs`, …) |

## Configuration and resync

The operator process resolves its runtime configuration once, at
startup: built-in defaults, then the `--config` file (YAML or JSON),
then `ML_OPERATOR_*` environment variables, then the flags
explicitly set on the command line (`internal/config`, precedence
defaults < file < env < flags). Only a flag you explicitly set
overrides the file or the environment, and a bad value in any
source fails startup instead of running on a configuration nobody
asked for. The startup line prints the effective configuration
(namespace, metrics address, resync interval, retry budget), and
the retry budget is applied to the reconciler before the first
reconcile.

Reconciles are also driven by a ticker, not only by events: the
resync loop re-reconciles every registered deployment on
`reconcileIntervalSeconds` (default 30s, `0` disables it), so drift
is caught without waiting for a watch event. A deployment the
resync cannot reconcile is logged to stderr and retried on the
next tick — the failure is never fatal to the operator, and the
steady-state resync of an in-sync deployment is a no-op that
records no history.

## The reconcile loop

`Reconciler.Reconcile` is level-triggered and idempotent — it
recomputes the desired state from the registered spec and the live
cluster every time, and applies only the difference:

1. **Defaults + validation.** `ApplyDefaults` fills replicas, port,
   strategy, resources, the health path, and the availability block
   (anti-affinity on, maxSkew 1, minAvailable left to resolve to
   replicas-1); `Validate` rejects the rest. An invalid spec never reaches the cluster — the deployment
   goes `Failed` with the validation errors in its status message.
   `Lint` runs alongside it on the spec as written (before
   defaults, so explicit choices stay visible) and warns about
   risky-but-valid specs — untagged or `:latest` images, a canary
   first step over 50%, a gate that can promote with pods unready.
   `mlop validate` exposes both checks without touching state,
   and `--strict` fails on warnings for CI gates. A paused
   manifest also warns: applying it freezes the deployment, so
   the warning is the tripwire against freezing one by accident.
   Pausing itself is step 1½, right after validation: a paused
   deployment returns early with its phase preserved and a
   `Paused: True` condition, before any rollout-state cleanup,
   autoscaling decision, or cluster write — so a mid-flight
   rollout resumes from its stored state.
2. **Rollout state.** A Canary strategy keeps per-generation rollout
   state (weight ladder, step, consecutive failures); a BlueGreen
   strategy keeps its own (soaking/switched, consecutive failures).
   Any other strategy drops both, so returning to Canary or
   BlueGreen starts fresh. A done state (promoted/folded or
   aborted) is kept, so reconciling the same generation never
   restarts the rollout.
3. **Autoscaling.** When enabled, the scaler decides the effective
   replica count from the latest cluster metrics (largest proposal
   wins, clamped to min/max, scale-down held for 5 minutes). The
   scaler owns the count — `spec.replicas` is only the starting
   point, HPA semantics.
4. **Render + plan.** `render` builds the desired Deployment,
   Service, HPA and PodDisruptionBudget. During a canary the stable Deployment keeps
   serving the previous image until promotion (only the image and
   version label are pinned); during a blue-green rollout the same
   pinning holds while green soaks, and the desired Service selects
   blue pods until the switch flips it to green. `plan` diffs
   desired vs live and returns the minimal ordered actions:
   Deployment, canary, green, Service, HPA, PDB. Recreate deletes
   before creating, so old and new model versions never serve side
   by side. A blue-green rollout only exists while a version change
   is in flight — a first deploy or a replica-only change applies
   directly, with no green Deployment at all.

   Hardening rides on the same render: the pod template carries
   preferred pod anti-affinity (hostname) and one topology-spread
   constraint (maxSkew, `ScheduleAnyway`), so a multi-replica model
   spreads across nodes by default; a single replica renders no
   spread constraint. The PDB selects the model's base labels, so
   it covers stable, canary and green pods alike, with
   minAvailable resolving to replicas-1 by default. A single
   replica renders no PDB at all — a budget that allows every
   disruption protects nothing — and scaling moves the PDB with
   the replica count (up updates minAvailable, down to one deletes
   the PDB). Deleting the deployment garbage-collects the PDB with
   the rest of its objects.
5. **Apply.** Each action executes against the `Cluster`
   interface through `retryApply`: a transient cluster error is
   retried with exponential backoff (3 attempts, 100ms base,
   doubling per retry) instead of failing the reconcile on the
   first blip. A recovered apply records one `ApplyRetried` event
   and one `ml_operator_apply_retries_total{outcome=recovered}`
   increment; a spent budget records `outcome=exhausted` and the
   deployment goes `Degraded` with requeue, not `Failed` — the
   spec is fine, the cluster operation broke. The rollout paths
   (canary promote / weight step / abort, blue-green switch /
   fold / abort) use the same helper, so no cluster write skips
   the retry policy. Applies are idempotent by construction (the
   same desired object applied twice is a no-op), so a retry
   never duplicates an object or resets its readiness.
6. **Gate (canary and blue-green).** The health gate reads the live
   canary's (or green Deployment's) readiness plus error rate, p95
   latency and the model-load probe from cluster metrics, against
   the rollout's own thresholds (`canary.gate` or `blueGreen.gate`,
   both defaulted to 5% errors / 500ms p95 / all pods ready). The
   canary state machine holds, advances, promotes or aborts (3
   consecutive failures); the blue-green one holds, switches, folds
   or aborts. A still-loading model holds without burning failure
   budget; a failed load is a real failure.
7. **Status.** The phase is recomputed from readiness every time
   (`Available` at full replicas, `Degraded` below, `Progressing`
   while converging) and the conditions are rebuilt, so a stale
   condition can never linger. An aborted rollout stays `Degraded`
   for its generation — readiness alone must not flip it back to
   `Available` while the stable Deployment serves the old version.
   Each condition carries a real reason (`InvalidSpec`,
   `ApplyFailed`, `RolloutAborted`, `ReplicasAvailable`, ...) and a
   `lastTransitionTime` stamped when its status last flipped, using
   the reconciler's clock: a no-op reconcile keeps the old stamp,
   so `mlop status` can say how long a deployment has been Degraded
   instead of just that it is. Conditions loaded from a state file
   written before transition times existed are stamped on first
   sight.

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

Blue-green runs a smaller machine — there is no weight ladder, so
traffic never splits:

```
   soak ◄── gate fails / model loading ─┐
     │                                  │
     ├─ gate passes ─► switch ─► fold   │
     │                                  │
     └─ 3 failures ─► abort ─────────────┘
```

- **Soak:** green runs at full size beside stable; the Service
  still selects blue pods, so clients never see green.
- **Switch:** gate passed — the Service selector flips to green.
  The switch is the commit point: the soak gate was the safety
  check.
- **Fold:** the next reconcile applies the new image to the stable
  Deployment, flips the Service back to blue (which is now the new
  version), deletes green, and records the replaced image as the
  rollback target — the same `status.previousImage` a canary
  promotion records.
- **Abort:** 3 consecutive gate failures — green is deleted, the
  Service never flipped, stable keeps serving the previous version,
  and the deployment stays `Degraded` until a new generation arrives.

## Generations and rollback

The generation is the CLI's job, never the manifest's: a new key
starts at 1, a changed spec bumps it, an identical re-apply keeps
it. The reconciler records the replaced image on every successful
image change (canary promotion, blue-green fold, or direct
rolling/recreate update),
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

## CRD and installation

The operator is installable on a real cluster before the
controller-runtime phase lands: `internal/crd` generates the
manifests from the same code the operator validates against, so
the installed CRD can never drift from the operator's rules.

- **CustomResourceDefinition** (`modeldeployments.ml.example.com`,
  `v1alpha1`, Namespaced, short name `mldep`): the openAPIV3
  schema for spec and status mirrors `api.Validate` field for
  field — port 1–65535, canary weight 1–99, gate fractions 0–1,
  strategy and phase enums — so the API server rejects a bad spec
  at admission time, before the operator ever sees it. The status
  subresource is enabled, so status is only ever written by the
  operator, never by an apply. `kubectl get mldep` shows Phase,
  Ready and Age columns.
- **Install bundle** (Namespace `ml-operator-system`,
  ServiceAccount, ClusterRole, ClusterRoleBinding, operator
  Deployment): RBAC is least privilege for what the reconciler
  does today — ModelDeployments plus their status, the
  Deployments/Services/HPAs it renders, read-only pods for
  readiness, and event creation. No secrets, no nodes, no
  cluster-admin. The Deployment runs one replica of
  `ghcr.io/karnchoudhary-99/ml-operator:v0.1.0` with
  `--namespace=default --metrics-addr=:8080
  --reconcile-interval=30s`, a `/healthz` liveness probe, and
  100m/128Mi requests.

Generate them, never hand-edit them:

```bash
mlop crd                               # the CRD alone
mlop crd | kubectl apply -f -
mlop install                           # Namespace + RBAC + Deployment
mlop install --namespace team-ml --image registry.example.com/ml-operator:v0.2.0 | kubectl apply -f -
```

The generated files are checked in under `examples/` (`crd.yaml`,
`install.yaml`) and golden-tested: a schema or install change
that forgets to regenerate fails the suite with the exact command
to run. The API group (`ml.example.com`) is a placeholder until
the project registers a real domain; changing it later is a
breaking change for every installed CRD.

Two things the schema language cannot express stay in
`api.Validate` instead: cross-field rules (autoscaling
`minReplicas <= maxReplicas`, "at least one scaling target") and
the gate rule that a zero threshold takes the operator default
rather than disabling its check. The schema is the first gate;
the operator's validation is the second.

## State and persistence

`mlop` persists the registry, the fake cluster, rollout state and
events to a JSON state file (`~/.mlop/state.json` by default) via
`internal/store`, atomically (temp file + rename). The autoscaler's
scale-down stabilization window is in-memory only and restarts on
every invocation — documented in `internal/store`. Deleting the
state file resets the simulated world; nothing in it is a source
of truth beyond the manifests.

`internal/backup` snapshots that file into a versioned envelope
(`mlop backup` / `mlop restore`): the raw state plus a SHA-256
of the canonical payload, the mlop version and a creation
timestamp. Restore verifies the checksum and refuses to clobber
a non-empty state without `--force`, so a bad restore cannot
silently drop a live world.
