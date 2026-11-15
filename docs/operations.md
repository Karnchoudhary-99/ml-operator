# Operations runbook

Day-to-day operation of the ML Model Operator through `mlop`.
All commands share one simulated world in the state file
(`~/.mlop/state.json`, or `--state` / `$MLOP_STATE`); separate
invocations share it, and deleting it resets the world.

## Installing on a cluster

The operator installs on a real cluster from generated manifests
(`mlop crd` and `mlop install`, both read-only — they print YAML
and touch no state):

```bash
mlop crd | kubectl apply -f -        # CustomResourceDefinition
mlop install | kubectl apply -f -    # Namespace + RBAC + operator Deployment
kubectl get mldep                     # Phase / Ready / Age columns
```

The operator lands in `ml-operator-system` and watches the
`default` namespace for ModelDeployments; override either with
`mlop install --namespace team-ml`, and the image with
`--image registry.example.com/ml-operator:v0.2.0`. The checked-in
copies live in `examples/crd.yaml` and `examples/install.yaml`
— apply those directly if you prefer files over pipes, and
regenerate them (`mlop crd > examples/crd.yaml`,
`mlop install > examples/install.yaml`) after any schema or
install change; the golden tests fail the suite if they drift.

Check the install before deploying a model:

```bash
kubectl -n ml-operator-system get pods   # operator Running, /healthz live
kubectl get crd modeldeployments.ml.example.com
```

Then deploy a model. The cluster takes the Kubernetes shape of
the same manifest — `apiVersion: ml.example.com/v1alpha1`,
`kind: ModelDeployment`, `metadata.name`, `spec` — not the
`name:`/`spec:` shorthand `mlop apply` reads
(`examples/iris-mlp.yaml`); `examples/iris-mlp-k8s.yaml` is that
shape for the same model:

```bash
kubectl apply -f examples/iris-mlp-k8s.yaml
kubectl get mldep iris-mlp               # Phase, Ready, Age
```

Note the honest limit: the reconciler behind the installed
operator still runs against the simulated cluster (see the
roadmap), so a cluster install today proves the CRD, RBAC and
operator packaging — it does not yet reconcile real Deployments.
That is the controller-runtime phase.

## Deploying a new model version

Preview first, then apply — `diff` never changes state:

```bash
mlop diff -f examples/iris-mlp-canary.yaml   # what would change, and why
mlop apply -f examples/iris-mlp-canary.yaml  # registers generation N+1, reconciles once
mlop rollout status iris-mlp                  # weight, step, gate failures
mlop status iris-mlp                          # phase + readiness
```

`mlop status` also lists the conditions with the reason each one
holds and how long it has held it (`Ready: True (ReplicasAvailable)
— 5m ago`), so a stuck deployment shows its age at a glance. The
same data is in `mlop status --json` as `lastTransitionTime` per
condition.

A canary advances only while the health gate passes (error rate,
p95 latency, model-load probe, canary readiness). While it is in
flight, `mlop status` re-reconciles on every call, so the rollout
moves as you watch it — but the step interval still gates
advances: a step does not jump early just because you polled.

A blue-green rollout works the same way, without the ladder:

```bash
mlop diff -f examples/iris-mlp-bluegreen.yaml   # green create, stable pinned
mlop apply -f examples/iris-mlp-bluegreen.yaml  # registers generation N+1, reconciles once
mlop rollout status iris-mlp                    # soaking / switched / folded / aborted
mlop ready iris-mlp --replicas 2 --green        # green pods pass the gate
mlop load iris-mlp --green                      # green model-load probe
```

While green soaks, `mlop status` still shows the old version
serving — that is the point. Once the gate passes, the Service
switches to green, and the next reconcile folds green into
stable. `mlop rollout status` shows the switch as `switched`
and the fold as `folded`; the fold records the replaced image as
the rollback target, exactly like a canary promotion. A first
deploy or a replica-only change under `BlueGreen` applies
directly, with no green Deployment at all.

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

## Reading the history

```bash
mlop history iris-mlp                  # oldest first, --tail N to limit
mlop history iris-mlp --json           # machine-readable, same trail
```

While `logs` answers "what notable thing happened last?",
`history` answers "what happened to this model, and when?" — one
entry per generation / phase / image transition, in chronological
order. A steady deployment that reconciles every few seconds
adds no entries: only real transitions are recorded (first
deploy, phase flip, version change, promotion, rollback, abort).
The trail is capped at 50 entries per deployment and persists in
the state file, so it survives across CLI invocations; deleting
a deployment drops its trail with it.

## Waiting for a rollout

```bash
mlop watch iris-mlp                      # exit 0 on Available, 1 on Failed/Degraded, 2 on timeout
mlop watch iris-mlp --interval 10s --timeout 10m
mlop watch --all                         # waits for every deployment at once
mlop watch iris-mlp --json               # one JSON object per phase transition
```

Watch polls the deployment, reconciling on every tick, and prints
only when the phase changes — a steady Progressing deployment
prints one line, not one per second. It pairs with `mlop ready`:
run `ready` in one shell to simulate pods coming up, `watch` in
another to wait for the rollout to land. Scripts should check the
exit code, not parse the text: 0 is Available, 1 is a failure
(Failed or Degraded), 2 is the timeout expiring first.

## Backing up and restoring state

Everything the operator knows lives in the one state file, so a
backup is a snapshot of that file — deployments, cluster,
rollout progress, events and history together:

```bash
mlop backup ~/iris-backup.json            # snapshot the world
mlop backup ~/iris-backup.json --force    # overwrite an existing backup file
mlop restore ~/iris-backup.json           # verify + restore into the state file
```

A backup prints its deployment count, creation time and a short
SHA-256 checksum, and the file itself records the mlop version
that wrote it. Restore verifies the checksum before it touches
the live state: a corrupted or hand-edited backup is refused
with a checksum mismatch, and nothing is written. Restore also
refuses to overwrite a state file that already holds deployments
— use `--force` when the overwrite is intended, e.g. after a
bad apply wiped the world. Deleting the state file first has the
same effect without the flag. Try it end to end with
`examples/backup-tour.sh` (backup → wipe → restore → tamper
check).

## Metrics to watch

The operator serves its Prometheus exposition on `--metrics-addr`
(default `:8080`): `curl localhost:8080/metrics` (and
`curl localhost:8080/healthz` for the liveness probe). The demo
prints the same exposition at the end (`go run ./cmd/operator
--demo`). The metric names below are the ones the reconciler records:

- `ml_operator_reconciles_total{result=available|progressing|degraded|failed}`
  — one increment per reconcile outcome. A rising `degraded`
  share means rollouts are stuck or replicas are short.
- `ml_operator_rollouts_total{result=promoted|aborted}` — canary
  and blue-green outcomes. Aborts deserve a look at the gate:
  `mlop logs` names the failing check.
- `ml_operator_scale_events_total{direction=up|down|held}` —
  autoscaling activity. Frequent `held` entries are normal: the
  5-minute scale-down stabilization window is doing its job.
- `ml_operator_apply_retries_total{outcome=recovered|exhausted}`
  — cluster operations that needed retries. A rising `recovered`
  share means the cluster is flaky (worth a look before it becomes
  an outage); `exhausted` means reconciles are degrading — check
  `mlop logs` for `ApplyFailed` and the cluster itself.
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

**Blue-green aborted, deployment `Degraded`.** Same story, one
difference: the Service never flipped, so no client ever saw the
new version. Look for `BlueGreenAborted` in `mlop logs <name>`,
fix the model or loosen `blueGreen.gate`, and apply a new
generation.

**Blue-green holds at soaking with no failure.** Either the model
is still loading (`mlop load <name> --green` reports the probe
state; a loading model holds without burning failure budget) or
green pods are not ready yet (`mlop ready <name> --replicas N
--green`). Both clear on their own.

**Canary holds at a weight with no failure.** Either the model is
still loading (`mlop load <name> --canary` reports the probe
state; a loading model holds without burning failure budget) or
the step interval has not elapsed yet. Both clear on their own.

**`apply` rejects the manifest.** Validation runs before anything
is registered — fix the reported errors (bad name, limit below
request, missing canary config, …) and re-apply. A rejected apply
changes nothing.

**`apply` fails and the deployment goes `Degraded` with
`ApplyFailed`.** The reconciler already retried the operation 3
times with backoff — this is not a first-blip failure, it is a
cluster that stayed broken (or a permission the operator lacks).
Fix the cluster side, then re-apply or wait for the requeue: the
spec is unchanged and valid, so the next reconcile retries the
same plan. `mlop logs` shows `ApplyRetried` entries for the
operations that recovered on their own — a rising count means a
flaky cluster, worth investigating before it becomes an outage.

**`status` shows a phase you do not expect after an edit.** `mlop
status` reconciles first, so it reports the post-reconcile state.
Check `mlop diff <name>` to see whether the live state still
differs from the desired state, and `mlop logs <name>` for the
last recorded transition.

**State file confusion.** If the simulated world gets into a state
you cannot explain, `mlop validate`, `mlop diff` and `mlop logs` are read-only and
safe to run. Deleting the state file resets everything — the
manifests are the source of truth, so re-applying them rebuilds
the world.

## Pre-flight checklist (before every apply)

1. `mlop validate -f <manifest>` — errors fail, warnings are listed;
   `--strict` fails on warnings too. In CI, gate the apply on it.
2. `mlop diff -f <manifest>` — read the plan, not just the phase.
2. For a canary or blue-green rollout: is the gate right for this
   model? A latency-sensitive model wants a tighter gate
   (`canary.gate` / `blueGreen.gate`, `maxP95LatencyMs`); a noisy
   one a looser `maxErrorRate`. Thresholds tune the gate, they
   never disable a check.
3. After apply: `mlop status` + `mlop rollout status` until the
   phase is `Available` (or the rollout is promoted).

## Operator configuration

The operator's runtime settings come from four sources, weakest to
strongest: built-in defaults, a config file (`--config`, YAML or
JSON — `examples/operator-config.yaml` is a commented starting
point), `ML_OPERATOR_*` environment variables, then CLI flags.
Only a flag you explicitly set overrides the file or the
environment. A bad value in any source fails startup with the
offending key named — check the startup line, which prints the
effective configuration (`namespace=… metrics=… resync=…s
retries=…@…ms`), before assuming the operator is running with the
settings you intended.

The resync re-reconciles every registered deployment on
`reconcileIntervalSeconds` (default 30s), so drift is caught without
waiting for an event. A deployment the resync cannot reconcile is
logged to stderr and retried on the next tick; it never kills the
operator. Set the interval to `0` to disable the resync, and the
metrics address to `""` to disable the metrics endpoint.
