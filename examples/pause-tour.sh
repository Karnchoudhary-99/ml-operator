#!/usr/bin/env bash
# mlop pause/resume tour: apply v1 and bring it Available, roll to
# v2 so a rollback target exists, pause the deployment, stage a v3
# change while paused (the reconciler holds it — the live
# Deployment keeps serving v2), then resume and watch v3 apply.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/pause-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply iris-mlp v1 and bring it Available"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "roll to v2, so a rollback target exists"
sed 's/iris-mlp:v1/iris-mlp:v2/; s/version: v1/version: v2/' examples/iris-mlp.yaml > "$WORK/iris-mlp-v2.yaml"
"$MLOP" --state "$STATE" apply -f "$WORK/iris-mlp-v2.yaml"
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE" status iris-mlp
expect "previous:   registry.example.com/iris-mlp:v1 (v1)" "$MLOP" --state "$STATE" status iris-mlp

step "pause the deployment"
expect "paused default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" pause iris-mlp
expect "paused:     yes" "$MLOP" --state "$STATE" status iris-mlp
expect "Paused: True" "$MLOP" --state "$STATE" status iris-mlp

step "pausing again is a no-op"
expect "already paused (generation 3)" "$MLOP" --state "$STATE" pause iris-mlp

step "stage v3 while paused: registered, not applied"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-paused-v3.yaml
expect "registry.example.com/iris-mlp:v3" "$MLOP" --state "$STATE" status iris-mlp
# The diff still shows the v2 -> v3 update as pending work, and
# says it applies on resume — proof the live Deployment still
# serves v2.
expect "is paused — this plan applies on resume" "$MLOP" --state "$STATE" diff iris-mlp
expect "UpdateDeployment default/iris-mlp" "$MLOP" --state "$STATE" diff iris-mlp

step "watch and rollback refuse a paused deployment"
# Capture first, then check: never pipe mlop straight into grep
# (SIGPIPE under pipefail — see tour-lib.sh).
out="$("$MLOP" --state "$STATE" watch iris-mlp --timeout 1s 2>&1 || true)"
printf '%s\n' "$out" | grep -q "is paused; resume it to make progress" || { echo "watch should refuse a paused deployment, got: $out" >&2; exit 1; }
out="$("$MLOP" --state "$STATE" rollback iris-mlp 2>&1 || true)"
printf '%s\n' "$out" | grep -q "is paused; resume it first" || { echo "rollback should refuse a paused deployment, got: $out" >&2; exit 1; }

step "resume: the held v3 change applies"
expect "resumed default/iris-mlp (generation 5)" "$MLOP" --state "$STATE" resume iris-mlp
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp
expect "Paused: False" "$MLOP" --state "$STATE" status iris-mlp

step "resuming again is a no-op"
expect "not paused (generation 5)" "$MLOP" --state "$STATE" resume iris-mlp

echo
echo "tour passed: pause froze v2, v3 staged while paused, resume applied v3."
