# Troubleshooting

Symptom-first guide for `mlop`. The short version of most entries:
the manifests are the source of truth and the state file is
disposable — back it up (`mlop backup`), delete it, re-apply, and
the world comes back. `mlop validate`, `mlop diff`, and `mlop logs`
are read-only and always safe to run while you diagnose.

## `mlop status` says "no deployments"

The state file has nothing registered. Either nothing was applied
yet, or the shell is looking at a different state file than the
one you applied to. The default is `~/.mlop/state.json`, or
`$MLOP_STATE` when set, or `--state` when passed. Compare:

```bash
mlop --state /path/you/used status
echo "$MLOP_STATE"
mlop __complete-names   # prints exactly what the shell sees
```

## `mlop status <name>` says "no deployment"

The name is resolved against the `default` namespace unless you
write `ns/name`. A deployment applied with `namespace: team-a` in
the manifest is `team-a/iris-mlp`, not `iris-mlp`. `mlop status`
with no name lists everything, which shows the qualified names.

## Stuck in `Progressing`, `0/N ready`

The fake cluster has no pods, so nothing becomes ready on its
own — `mlop ready <name> --replicas N` simulates pods passing
the readiness gate. Until then the reconciler keeps requeueing
and the phase stays `Progressing`, which is correct behaviour,
not a hang. In the real-cluster phase this maps to checking pod
readiness directly.

## `apply` rejects the manifest

Validation runs before anything is registered, so a rejected
apply changes nothing. Fix the reported errors and re-apply. The
usual ones:

- **bad name** — names must be DNS-1123 (lowercase letters,
  digits, `-`, start and end alphanumeric).
- **limit below request** — a resource limit must be >= its
  request; the error names the resource.
- **missing canary config** — `strategy: Canary` needs a `canary`
  block (weight 1–99, steps, interval); `strategy: BlueGreen`
  needs a `blueGreen` block.
- **`:latest` tag** — allowed, but warned about; `--strict`
  turns the warning into a failure on `mlop validate`.
- **uid 0 with runAsNonRoot on** — `spec.security.runAsUser: 0`
  while `runAsNonRoot` is true (the default) is a contradiction:
  the kubelet would refuse to start the pod, so the operator
  refuses the manifest instead. Set `runAsNonRoot: false` to run
  as root, or pick a non-zero uid.
- **placement mistakes** — a toleration `value` with operator
  `Exists` (Exists matches on the key alone), `tolerationSeconds`
  without effect `NoExecute`, or a `nodeSelector` key that is not
  a valid label key (letters, digits, `-`, `_`, `.`, with an
  optional DNS prefix before `/`). The error names the field.
- **modelSource mistakes** — an `s3://` (or other non-http(s))
  URI (only http(s) downloads are supported — the init container
  is curl, and credentialled stores are rejected at apply time,
  not left to fail the pod), a `sha256` that is not 64 hex
  characters, or a `mountPath` that is not an absolute path. The
  error names the field. A missing checksum is only a warning
  (`mlop validate` flags the unverified download), not an error.

## `apply` fails and the deployment goes `Degraded` with `ApplyFailed`

The reconciler already retried the operation 3 times with
backoff, so this is not a first-blip failure — the cluster stayed
broken (or a permission is missing). Fix the cluster side, then
re-apply or wait for the requeue: the spec is unchanged and valid,
so the next reconcile retries the same plan. `mlop logs` shows
`ApplyRetried` entries for operations that recovered on their
own; a rising count means a flaky cluster, worth investigating
before it becomes an outage.

## Canary aborted, deployment `Degraded`

The stable Deployment still serves the previous version — traffic
was never lost, the new version simply did not promote. Read the
gate reason in `mlop logs <name>` (look for `RolloutAborted`),
fix the model or loosen the gate in the manifest
(`canary.gate`), and apply a new generation. The aborted
generation never restarts on its own.

## Blue-green aborted, deployment `Degraded`

Same story, one difference: the Service never flipped, so no
client ever saw the new version. Look for `BlueGreenAborted` in
`mlop logs <name>`, fix the model or loosen `blueGreen.gate`,
and apply a new generation.

## Canary holds at a weight / blue-green holds at soaking, no failure

Either the model is still loading (`mlop load <name> --canary` /
`--green` reports the probe state; a loading model holds without
burning failure budget) or the step interval has not elapsed yet
(canary) or green pods are not ready yet (`mlop ready <name>
--replicas N --green`). All three clear on their own.

## `mlop rollback` refuses

- **"no previous version to roll back to"** — the image has
  never been replaced; there is nothing to restore. Rollback only
  exists after a version change has landed.
- **"already serves the previous version"** — the rollback
  already happened and nothing newer has promoted since. Apply a
  new version first.
- **Paused deployments cannot roll back** — `mlop resume <name>`
  first; rollback reconciles, and reconciliation is suspended
  while paused.

## `mlop scale` refuses

- **\"has autoscaling enabled\"** — the autoscaler owns the
  replica count: the reconciler overwrites `spec.replicas` with
  the scaler's decision on every reconcile, so a manual scale
  would be reverted on the next resync. Change
  `spec.autoscaling.minReplicas`/`maxReplicas` instead (or turn
  autoscaling off in the manifest and re-apply).
- **\"is paused; resume it first\"** — same as rollback: a scale
  is a new generation that must reconcile, and reconciliation is
  suspended while paused.
- **\"--replicas N is required\"** — the flag is mandatory; a
  negative value is rejected the same way.

## Deployment waits for its maintenance window

`mlop status <name>` says `waiting for maintenance window —
next opens Tue 02:00 UTC` with a `WindowBlocked: True`
condition. This is the operator working as designed, not a
stall: the spec's `maintenanceWindow` is closed right now, so
the change (a new image, a scale, a rollout step) is deferred
until the window opens, and then applies on the next reconcile.
Check the window line in status — it names the schedule and
whether it is open now. If the change cannot wait, remove the
`maintenanceWindow` block from the manifest and re-apply (that
is the emergency override), or use `mlop repair` for drift
repairs, which apply immediately by design. If the window looks
wrong, check the obvious mistakes first: days are three-letter
abbreviations (`Mon`, not `Monday`), times are 24-hour UTC
(`02:00`, not `2pm`), and an end earlier than the start means
overnight — a `22:00`–`02:00` window on Monday covers Tuesday
01:00. `mlop validate` rejects all three mistakes at apply time.

## `mlop watch` exits 2

Exit codes: `0` means the deployment reached `Available`, `1`
means it reached `Failed` or `Degraded`, `2` means the timeout
expired first (default 5 minutes) — or the usage was wrong.
A timeout is not an error in the deployment: check `mlop status
<name>` and `mlop logs <name>`, then watch again with a longer
`--timeout`, or drive readiness with `mlop ready` while it
watches.

## `status` shows a phase you do not expect after an edit

`mlop status` reconciles first, so it reports the post-reconcile
state. Check `mlop diff <name>` to see whether the live state
still differs from the desired state, and `mlop logs <name>` for
the last recorded transition. A re-apply of an unchanged manifest
is a deliberate no-op — same generation, nothing to reconcile —
so if you expected a change, the manifest did not actually change
(check the image tag and version, not just the file's mtime).

## `mlop repair` exits 1 with objects "left"

Exit 1 is the honest answer, not a failure: something is still
unrepaired. Read the lines — each one says why:

- **left (unexpected)** — an object exists that nothing desires
  (a leftover canary, an HPA after autoscaling was turned off).
  Repair never deletes these; the deletion is your call. Delete
  the object yourself, or update the manifest if the live object
  is the intended state, then repair again. Note that `mlop
  status` reconciles, and the reconciler *does* garbage-collect
  leftovers — so a status check between repair and drift can make
  the unexpected object vanish on its own.
- **left (drifted/missing) with a paused note** — the deployment
  is paused, and repair writes nothing while paused. `mlop resume`
  first; resume reconciles immediately, so the drift may already
  be gone by the time you repair.
- **FAILED** — the cluster write errored after the retry budget.
  Fix the cluster side and repair again; the other objects were
  still attempted.

## `mlop repair --dry-run` exits 1 on a clean-looking deployment

A dry run exits 1 whenever it *would* have repaired something —
that is the CI gate, the same as `mlop drift` exiting 1. It writes
nothing (the state file is byte-identical afterwards), so a 1
here means "there is drift", not "the dry run broke something".
Run `mlop drift` to see the drift, then decide: repair it, or
update the manifest when the live change was the intended one.

## Commands fail with a JSON error about the state file

The state file is corrupt (a partial write, or a hand-edit gone
wrong). `mlop` refuses to guess at it. Recover:

```bash
mlop backup ~/state-rescue.json   # fails too if the file is unreadable — skip if so
cp ~/.mlop/state.json ~/state-corrupt.json   # keep the evidence
rm ~/.mlop/state.json
mlop apply -f <your manifests>    # rebuilds the world from the manifests
```

If you have a backup, `mlop restore` it instead — restore
verifies the checksum first and refuses a tampered file, so a
successful restore is a known-good state.

## Shell completion offers nothing (or stale names)

The scripts call bare `mlop` — check that the `mlop` on your
`PATH` is the one you built (`which mlop`, `mlop version`).
Names come from the state file, so a completion that offers
nothing usually means the shell is reading a different state file
than the one you applied to: check `--state` / `$MLOP_STATE`, and
run `mlop __complete-names` directly — it prints exactly what the
shell sees. After rebuilding mlop, re-source the script; the
installed copy is a snapshot, not a link to the binary.

## The operator binary will not start

`operator --version` prints `0.1.0` when the build is fine. For
anything else, start from the flags: `--demo` runs the reconciler
end to end against the fake cluster with no other setup, which
separates a binary problem from a configuration problem. Config
resolution order is flags > environment > config file > defaults
(see `docs/operations.md`, "Operator configuration").
