# Changelog

All notable changes to ml-operator, newest first. The project is
pre-1.0 and unreleased, so everything lands under Unreleased until
the first public version is cut.

## Unreleased

### Added

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

- Re-registration no longer wipes stored status (registry).
- `SpecsEqual` no longer ignores availability changes, so a
  PDB is updated when only the availability block changes.
- Canary renders with a canary-only selector, so the stable
  Deployment cannot adopt canary pods.
- Tour scripts capture mlop's output before checking it — piping
  into `grep -q` caused flaky SIGPIPE failures (exit 141).
