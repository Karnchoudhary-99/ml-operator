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

## Scaling replicas

```bash
mlop scale iris-mlp --replicas 4   # generation bump, reconciles immediately
mlop scale iris-mlp --replicas 4   # already at 4: no-op, no generation bump
mlop scale iris-mlp --replicas 1 --dry-run   # prints the change, writes nothing
mlop scale iris-mlp --replicas 0   # stops serving; status reports 0/0 replicas ready
```

A pure replica change needs no rollout: the Deployment is updated
in place and the phase follows the new count. Two refusals keep
the command honest. With autoscaling on, the autoscaler owns the
count — the reconciler overwrites `spec.replicas` with the
scaler's decision on every reconcile — so `mlop scale` refuses
and points at `spec.autoscaling.minReplicas`/`maxReplicas`
instead; a manual scale would otherwise be silently reverted on
the next resync. And a paused deployment refuses a scale the same
way it refuses a rollback: resume first, or the new generation
would be swallowed. Every manual scale records a `Scaled` event
in `mlop logs`, so the audit trail shows who changed the count.

## Pausing and resuming

```bash
mlop pause iris-mlp    # suspend reconciliation (generation bumps)
mlop resume iris-mlp   # clear the pause and reconcile immediately
```

Pausing is the incident-freeze switch: while a deployment is
paused the operator makes no cluster changes, advances no rollout,
and applies no autoscaling decision — the live objects keep
serving exactly as they are, and the deployment keeps its phase
with a `Paused: True` condition. A canary or blue-green rollout
paused mid-flight resumes from where it stopped instead of
restarting. `mlop watch` and `mlop rollback` refuse a paused
deployment rather than waiting on a phase that cannot arrive or
silently swallowing the new generation.

The same flag lives in the manifest (`spec.paused: true`), so a
change can be staged while paused: apply the manifest with
`paused: true` and the new image registers and bumps the
generation but does not apply — `mlop diff` shows the pending
plan with a note that it applies on resume. One sharp edge:
apply replaces the whole spec, so a manifest *without* `paused`
resumes the deployment (absent means false). To change the spec
while staying paused, the manifest must set `paused: true` —
`mlop apply` and `mlop validate` both warn about it.

## Detecting drift

```bash
mlop drift iris-mlp            # exit 0 when in sync, 1 when drift is found
mlop drift --all               # every registered deployment
mlop drift iris-mlp --json     # machine-readable report
```

Drift is the live cluster disagreeing with the desired state: a
`kubectl edit` that swapped the image, a manual scale, a deleted
Service, a leftover canary Deployment from a rollout that never
cleaned up. The report names the object and the field that moved —
`image "…:v99" -> "…:v1"`, `replicas 1 -> 2` — per Deployment,
Service, HPA and PodDisruptionBudget, and says whether each object
is in sync, drifted, missing, or unexpected (present although
nothing desires it). During a canary or blue-green rollout the
fields the rollout itself is moving are not reported as drift: the
stable image is pinned to the live one until promotion, exactly as
the apply plan does. A paused deployment still reports drift, with
a note that nothing will correct it until resume.

Drift never changes anything — it is a read, so it is safe in CI
(the exit code is the gate: 0 in sync, 1 drift found, 2 usage
error). The fix is a decision: when the live change was a mistake,
repair it (next section); when the live change was the intended one
and the spec is what is stale, update the manifest instead.
`examples/drift-tour.sh` walks through both: an image swap, a
deleted Service, and a manual scale-down, each caught by drift and
fixed by a re-apply.

## Repairing drift

```bash
mlop repair iris-mlp                  # exit 0 when everything is repaired
mlop repair iris-mlp --dry-run        # report what would change, write nothing (exit 1 when it would repair)
mlop repair --all                     # every registered deployment
mlop repair iris-mlp --json           # machine-readable report
```

Repair is the write half of drift: it re-applies the desired state
to every drifted or missing object — the same apply plan the
reconciler uses, so repair and reconcile can never disagree about
what "correct" looks like — then re-checks drift to verify the fix.
It records a `DriftRepaired` event, visible in `mlop logs`.

Three deliberate limits, all safety:

- **Repair never deletes.** An unexpected object — a leftover
  canary, an HPA after autoscaling was turned off — might still be
  serving traffic, or might be evidence the manifest is stale.
  Repair reports it as left as-is and leaves the deletion to you
  (or to a reconcile, which owns that decision).
- **A paused deployment is refused.** Repair writes nothing while
  paused; resume first.
- **Repair never advances a rollout.** It fixes objects, not state
  machines: a canary in flight keeps its pinned stable image,
  exactly as drift reports it.

Use `--dry-run` in CI the same way as `mlop drift`: it exits 1 when
it would have repaired something, so a pipeline can fail on drift
and let a human decide whether to repair or to update the manifest.
`examples/repair-tour.sh` walks through the loop end to end: an
image swap, a deleted Service, and a leftover canary — the first
two repaired, the canary reported and left standing.

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

## Applying a directory of manifests

```bash
mlop apply -d examples/multi-model            # every .yaml/.yml/.json in filename order
mlop apply -d examples/multi-model --dry-run   # validate all, touch no state
```

A batch loads state once and saves once at the end. One bad file
does not abort the batch: it is reported, the rest still apply,
and the run ends with `summary: N applied, M unchanged, K failed`
and exit 1 when anything failed. Two files defining the same
deployment key in one directory are rejected as a duplicate (the
second file is named with the first); re-applying a key that
already exists in state is still fine — that is a normal
generation bump. Files that are not manifests are ignored, so a
README can live next to the manifests. `examples/batch-tour.sh`
drives it end to end: dry-run, apply, re-apply, then
`mlop watch --all` for both models.

## Deployment hardening

A model with more than one replica is hardened by default — no
manifest block needed:

- **PodDisruptionBudget.** The operator renders a PDB with
  `minAvailable` one below the replica count, so a node drain can
  always take one pod but never the whole model. It selects the
  model's base labels, so canary and green pods count toward the
  available count while they serve. A single replica renders no
  PDB — a budget that allows every disruption protects nothing.
- **Pod anti-affinity + topology spread.** Pods of one deployment
  prefer different nodes (hostname anti-affinity, `ScheduleAnyway`
  so a tight cluster still schedules), with a maxSkew of 1: at most
  one more pod on the busiest node than on the quietest.

Override the defaults in the manifest (`examples/iris-mlp-hardened.yaml`):

```yaml
spec:
  replicas: 3
  availability:
    minAvailable: 2
    antiAffinity: true
    maxSkew: 1
```

`minAvailable` must stay below the replica count — equal to it
would block every voluntary eviction, so `apply` rejects it.
`mlop status <name>` shows the effective values
(`availability: minAvailable 2/3, anti-affinity on, maxSkew 1`),
and `mlop diff` previews a change before it applies. Scaling
moves the PDB with the replica count: scale up updates
minAvailable, scale down to one replica deletes the PDB, and
deleting the deployment removes it with the rest of its objects.
`examples/hardening-tour.sh` drives all of it end to end.

## Pod security

Every serving Deployment is hardened by default — no manifest block
needed: pods run as non-root uid 10001, the container's root
filesystem is read-only, privilege escalation is off, all Linux
capabilities are dropped, and the pod runs under the RuntimeDefault
seccomp profile. The context renders onto the stable, canary and
green Deployments alike, so a rollout never silently drops it.
Override it per deployment with `spec.security`
(`examples/iris-mlp-secured.yaml`): `runAsUser` picks the uid,
`dropCapabilities` lists exactly the capabilities to drop (an
explicit list replaces the default `["ALL"]`; the YAML dialect has
no lists, so it is a comma-separated string there and a real array
in JSON manifests), `seccompProfile` is `RuntimeDefault`,
`Unconfined`, or `Localhost/<profile>`.

Relaxations are allowed but never silent: `mlop validate` warns
about every one (`runAsNonRoot: false`, `readOnlyRootFilesystem:
false`, `allowPrivilegeEscalation: true`, `seccompProfile:
Unconfined`, an empty capability list), and `--strict` turns the
warnings into failures for CI. A contradiction is rejected at
apply time: `runAsUser: 0` with `runAsNonRoot: true` would make the
kubelet refuse to start the pod, so the operator refuses the
manifest instead. `mlop status <name>` shows the effective values
and `mlop diff` names each moved field before it applies.
`examples/security-tour.sh` drives all of it end to end.

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

**Shell completion offers nothing (or stale names).** The scripts
call bare `mlop` — check that the `mlop` on your `PATH` is the one
you built (`which mlop`, `mlop version`). Names come from the
state file, so a completion that offers nothing usually means the
shell is reading a different state file than the one you applied
to: check `--state` / `$MLOP_STATE` (run `mlop __complete-names`
directly — it prints exactly what the shell sees). After
rebuilding mlop, re-source the script; the installed copy is a
snapshot, not a link to the binary.

## Pre-flight checklist (before every apply)

1. `mlop validate -f <manifest>` — errors fail, warnings are listed;
   `--strict` fails on warnings too. In CI, gate the apply on it.
2. `mlop diff -f <manifest>` — read the plan, not just the phase.
3. For a canary or blue-green rollout: is the gate right for this
   model? A latency-sensitive model wants a tighter gate
   (`canary.gate` / `blueGreen.gate`, `maxP95LatencyMs`); a noisy
   one a looser `maxErrorRate`. Thresholds tune the gate, they
   never disable a check.
4. For a multi-replica model: is the availability block right?
   `minAvailable` must be below the replica count, and turning
   anti-affinity off puts every pod at the mercy of one node.
5. After apply: `mlop status` + `mlop rollout status` until the
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

## Shell completion

`mlop completion <bash|zsh|fish>` prints a completion script for
your shell. It is generated from the CLI's own command table
(internal/completion), so it stays in step with the commands as
they evolve. It completes command names, per-command flags, files
for `-f`/`-d`, and deployment names — live, from the state file —
and `rollout` completes its `status` subcommand first.

```bash
mlop completion bash | sudo tee /etc/bash_completion.d/mlop   # system-wide
source <(mlop completion bash)                                # this shell only
mlop completion zsh > "${fpath[1]}/_mlop"                     # then compinit
mlop completion fish > ~/.config/fish/completions/mlop.fish
```

If names do not complete, see the troubleshooting entry above —
the usual cause is a different `mlop` on `PATH` or a different
state file than the one you applied to.

## End-to-end tours

The fourteen scripts in `examples/*-tour.sh` are the CLI's end-to-end
smoke tests: each one drives a whole story through `mlop` (apply,
ready, canary or blue-green rollout, rollback, scale, pause/resume,
backup/restore, batch apply, hardening, shell completion) and asserts the output at
every step, so a tour is a demo and a test in one. Run one directly
(`examples/mlop-tour.sh`) or all of them; CI runs all fourteen on
every push and PR.

All tours share `examples/tour-lib.sh`, which provides `expect`
(run a command, capture its output, fail unless it matches a
pattern) and `expect_lines` (fail unless the output has exactly
that many lines). Assertions capture mlop's output first and only
then check it — never pipe mlop straight into `grep -q`, because
grep exits on the match while mlop still writes the lines after
it, and under `set -o pipefail` that SIGPIPE kills the tour.
A failed assertion prints the full captured output, so a red tour
says what mlop actually printed.
