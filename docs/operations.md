# Operations runbook

Day-to-day operation of the ML Model Operator through `mlop`.
All commands share one simulated world in the state file
(`~/.mlop/state.json`, or `--state` / `$MLOP_STATE`); separate
invocations share it, and deleting it resets the world.

## Deploying a new model version

Preview first, then apply — `diff` never changes state:

```bash
mlop diff -f examples/iris-mlp-canary.yaml   # what would change, and why
mlop apply -f examples/iris-mlp-canary.yaml  # registers generation N+1, reconciles once
mlop rollout status iris-mlp                  # weight, step, gate failures
mlop status iris-mlp                          # phase + readiness
```

A canary advances only while the health gate passes (error rate,
p95 latency, model-load probe, canary readiness). While it is in
flight, `mlop status` re-reconciles on every call, so the rollout
moves as you watch it — but the step interval still gates
advances: a step does not jump early just because you polled.

## Rolling back

```bash
mlop status iris-mlp        # shows the rollback target ("previous: …")
mlop rollback iris-mlp      # restores it as a new generation
```

Rollback refuses when there is no previous version, or when the
deployment already serves it. A rollback is itself a rollout
through the deployment's normal strategy — it does not edit
history, so a second rollback returns to the version just left.

## Reading the log

```bash
mlop logs iris-mlp                  # newest first, --tail N to limit
mlop logs iris-mlp --json           # machine-readable, same events
```

Only notable transitions are recorded — promotions, scale
decisions, aborts, invalid specs — not routine reconciles. A
repeated identical event aggregates with a count (`(x3)`), the
way Kubernetes aggregates events.

## Metrics to watch

The demo prints the reconciler's Prometheus exposition at the end
(`go run ./cmd/operator --demo`); serving it on `--metrics-addr`
(default `:8080`) lands with the controller-runtime phase — the
flag is parsed today, the endpoint is not yet wired. The metric
names below are already the ones the reconciler records:

- `ml_operator_reconciles_total{result=available|progressing|degraded|failed}`
  — one increment per reconcile outcome. A rising `degraded`
  share means rollouts are stuck or replicas are short.
- `ml_operator_rollouts_total{result=promoted|aborted}` — canary
  outcomes. Aborts deserve a look at the gate: `mlop logs` names
  the failing check.
- `ml_operator_scale_events_total{direction=up|down|held}` —
  autoscaling activity. Frequent `held` entries are normal: the
  5-minute scale-down stabilization window is doing its job.
- `ml_operator_ready_replicas` / `ml_operator_desired_replicas`
  — the gap between them is the deployment's health at a glance.

## Troubleshooting

**Stuck in `Progressing`, `0/N ready`.** The fake cluster has no
pods, so nothing becomes ready on its own — `mlop ready <name>
--replicas N` simulates pods passing the gate. In the real
cluster phase this maps to checking pod readiness directly.

**Canary aborted, deployment `Degraded`.** The stable Deployment
still serves the previous version — traffic was never lost, the
new version simply did not promote. Read the gate reason in
`mlop logs <name>` (look for `RolloutAborted`), fix the model or
loosen the gate in the manifest (`canary.gate`), and apply a new
generation. The aborted generation never restarts on its own.

**Canary holds at a weight with no failure.** Either the model is
still loading (`mlop load <name> --canary` reports the probe
state; a loading model holds without burning failure budget) or
the step interval has not elapsed yet. Both clear on their own.

**`apply` rejects the manifest.** Validation runs before anything
is registered — fix the reported errors (bad name, limit below
request, missing canary config, …) and re-apply. A rejected apply
changes nothing.

**`status` shows a phase you do not expect after an edit.** `mlop
status` reconciles first, so it reports the post-reconcile state.
Check `mlop diff <name>` to see whether the live state still
differs from the desired state, and `mlop logs <name>` for the
last recorded transition.

**State file confusion.** If the simulated world gets into a state
you cannot explain, `mlop diff` and `mlop logs` are read-only and
safe to run. Deleting the state file resets everything — the
manifests are the source of truth, so re-applying them rebuilds
the world.

## Pre-flight checklist (before every apply)

1. `mlop diff -f <manifest>` — read the plan, not just the phase.
2. For a canary: is the gate right for this model? A
   latency-sensitive model wants a tighter `canary.gate`
   (`maxP95LatencyMs`); a noisy one a looser `maxErrorRate`.
   Thresholds tune the gate, they never disable a check.
3. After apply: `mlop status` + `mlop rollout status` until the
   phase is `Available` (or the rollout is promoted).
