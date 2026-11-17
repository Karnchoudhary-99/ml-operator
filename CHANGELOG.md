# Changelog

All notable changes to ml-operator, newest first. The project is
pre-1.0 and unreleased, so everything lands under Unreleased until
the first public version is cut.

## Unreleased

### Added

- Model source — `spec.modelSource` (uri, optional sha256,
  mountPath, downloaderImage): the pod downloads the model
  artifact from an http(s) URI into a shared emptyDir volume at
  startup, instead of baking the weights into the serving image.
  A `model-download` init container (curl image by default)
  fetches the artifact and verifies it with `sha256sum -c` when a
  checksum is set, so a bad download fails the pod instead of
  serving silently; the serving container reads the artifact from
  `MODEL_ARTIFACT_PATH` unless the manifest sets it explicitly.
  Only http(s) URIs are supported (S3 and other credentialled
  stores are rejected at apply time). The source renders onto the
  stable, canary and green Deployments alike, `mlop validate`
  warns about an unverified download or a `/` mount path, `mlop
  status` shows the source and its verification state, `mlop
  diff` names a source change, and `examples/model-source-tour.sh`
  walks the whole story end to end.
- Image pull secrets — `spec.imagePullSecrets`: the Kubernetes
  secrets holding registry credentials for pulling the serving
  image. The YAML dialect writes a comma-separated string
  (`regcred, ghcr-creds`) and JSON carries a real array; empty
  means the pods pull without credentials. The secrets render
  onto the pod template of the stable, canary and green
  Deployments alike, `mlop status` shows them, `mlop diff` names
  a rotation, and the state round trip preserves them in both
  the registry spec and the rendered Deployment, so an operator
  restart never drops the credentials. `examples/image-pull-secrets-tour.sh`
  walks the whole story end to end.
- Traffic simulation — `mlop traffic <name> [--canary | --green]
  --requests N [--errors E] [--p95-ms M]`, feeding serving-traffic
  observations into the cluster metrics so the canary and
  blue-green health gates finally judge error rate and p95 latency
  instead of staying readiness-only. Batches accumulate into
  cumulative counters (the gate judges the cumulative error rate,
  so a bad batch keeps failing until healthy batches dilute it),
  the latest p95 wins, and the model-load probe and CPU state
  recorded alongside survive. `mlop status` shows the observation
  for the stable Deployment (plus canary/green lines while those
  rollouts are in flight), `--json` carries the same numbers, and
  `examples/traffic-tour.sh` walks both stories end to end: clean
  traffic promotes the canary, 20% errors abort it after three
  failed gates.
- Pod placement — `spec.placement` (nodeSelector + tolerations),
  pinning a model's pods to nodes: a GPU model lands on a GPU
  node (labelled, usually tainted) instead of anywhere the
  scheduler fancies, and a rollout never silently moves it off
  — canary and green Deployments inherit the placement. YAML
  manifests write tolerations as a comma-separated compact string
  (`gpu=true:NoSchedule, dedicated:Exists:NoExecute:300`); JSON
  carries a real array of toleration objects. `mlop validate`
  warns about the broad cases (an Exists toleration with an empty
  key, a `NoExecute` toleration with no grace period) and rejects
  the contradictions at apply time (a value with operator Exists,
  seconds without `NoExecute`). `mlop status` shows the placement
  and `mlop diff` names a nodeSelector or tolerations change, and
  `examples/placement-tour.sh` walks the whole story end to end.
- Maintenance windows — `spec.maintenanceWindow` (weekly UTC
  schedule: days as three-letter abbreviations, `start`/`end`
  as `"HH:MM"`, overnight windows allowed), deferring every
  reconcile that would change the cluster while the window is
  closed — no applies, no rollout step, no promotion — with a
  `WindowBlocked` condition naming the next opening and the
  phase preserved. An in-sync deployment is not blocked, a
  canary resumes from its step when the window opens, and
  `mlop repair` still applies immediately as an explicit human
  action. `mlop status` shows the window and whether it is open
  now, the CRD carries the new field, and
  `examples/window-tour.sh` walks the whole story end to end.
- Manual replica scaling — `mlop scale <name> --replicas N
  [--dry-run]`, a spec change that bumps the generation and
  reconciles immediately with no rollout. Scaling to the current
  count is a no-op; scaling to 0 stops serving; every manual scale
  records a `Scaled` event. It refuses when autoscaling is enabled
  (the autoscaler owns replicas and would revert the change on the
  next reconcile) and when the deployment is paused. `mlop
  completion` covers the new command, and `examples/scale-tour.sh`
  walks the whole story end to end.
- Pod security hardening — `spec.security`, hardened by default:
  non-root uid 10001, read-only root filesystem, no privilege
  escalation, all Linux capabilities dropped, RuntimeDefault
  seccomp, rendered onto the stable, canary and green Deployments
  alike. An explicit block overrides per field; `mlop validate`
  warns about every relaxation, `--strict` turns warnings into
  failures, and a contradiction (uid 0 with runAsNonRoot on) is
  rejected at apply time. `mlop status` shows the effective values
  and `mlop diff` names each moved field.
- Drift repair — `mlop repair <name> | mlop repair --all
  [--dry-run] [--json]`, the write half of drift: it re-applies
  the desired state to drifted and missing objects using the same
  apply plan the reconciler uses, then re-checks drift to verify.
  It never deletes unexpected objects (reported as left as-is),
  refuses paused deployments, and never advances a rollout; a
  dry run exits 1 when it would repair something, so it gates CI
  like drift does.
- Drift detection — `mlop drift <name> | mlop drift --all
  [--json]`, exit 0 in sync / 1 drift found / 2 usage error;
  per-object verdicts (in sync / drifted / missing / unexpected)
  with one reason per moved field, sharing the desired-state
  computation with the apply plan.
- Shell completion for `mlop` — `mlop completion <bash|zsh|fish>`
  generates the script from the CLI's own command table, so it
  completes command names, per-command flags, files for `-f`/`-d`,
  and deployment names live from the state file (through the
  hidden `mlop __complete-names` helper).
- Pause / resume reconciliation — `spec.paused` plus `mlop pause`
  / `mlop resume`; a paused deployment keeps serving while the
  operator stops reconciling it.
- Deployment hardening — PodDisruptionBudget, pod anti-affinity,
  and topology spread (`spec.availability`), rendered and applied
  by the reconciler.
- Batch apply — `mlop apply -d dir` applies every manifest in a
  directory in filename order, with per-file errors and a summary.
- Wait for a rollout — `mlop watch`, text and JSON stream,
  `--all` for every deployment.
- State backup + restore — `mlop backup` / `mlop restore`,
  checksummed, tamper-detected.
- CRD + install manifests — `mlop crd` / `mlop install`,
  golden-tested against the checked-in copies.
- Operator configuration — config file + environment + flags,
  periodic resync, retry budget.
- Status conditions with reasons and transition times; `mlop
  status` shows how long each condition has held.
- Deployment history audit trail — `mlop history`
  (generation/phase/image transitions).
- Metrics HTTP endpoint — `--metrics-addr`, `/metrics` +
  `/healthz`; Prometheus metrics and Kubernetes events.
- Preflight validation — `mlop validate`, warnings plus
  `--strict`.
- Diff preview + JSON output — `mlop diff`, `status`/`logs
  --json`.
- Rollback to the previous version — `mlop rollback`.
- Health + readiness gates — model-load probe (`mlop load`),
  per-rollout gate thresholds (`canary.gate`).
- Autoscaling — HPA from CPU / request-rate / GPU signals.
- Blue-green rollouts — green soaks at full size, Service
  cutover, fold.
- Rollout strategies — recreate, rolling update, canary,
  blue-green.
- Reconciler core — desired vs live state, diff, minimal apply
  plans, garbage collection on delete.
- ModelDeployment CRD types + validation, in-memory registry
  with watch events, Deployment + Service rendering.

### Fixed

- The fake cluster now clamps the ready count when a Deployment
  is re-applied with fewer replicas, so a scale-down (and a scale
  to 0) no longer reports the terminated pods as ready.
- Re-registration no longer wipes stored status (registry).
- `SpecsEqual` no longer ignores availability changes, so a
  PDB is updated when only the availability block changes.
- Canary renders with a canary-only selector, so the stable
  Deployment cannot adopt canary pods.
- Tour scripts capture mlop's output before checking it — piping
  into `grep -q` caused flaky SIGPIPE failures (exit 141).
