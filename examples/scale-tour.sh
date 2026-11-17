#!/usr/bin/env bash
# mlop scale tour: apply v1 at 2 replicas -> scale up to 4 -> no-op
# at 4 -> dry-run a scale down (state unchanged) -> scale down to
# 1 -> scale to 0 -> refusals (autoscaling owns replicas; paused
# deployments refuse). Driven entirely through the CLI; every step
# asserts the expected output, so the script is a demo and a smoke
# test in one.
#
# Usage: examples/scale-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply iris-mlp v1 at 2 replicas"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "scale up to 4"
expect "scaled default/iris-mlp (generation 2): 2 -> 4 replicas" "$MLOP" --state "$STATE" scale iris-mlp --replicas 4
# The 2 ready pods stay ready after the scale; the new target is 4.
expect "2/4 replicas" "$MLOP" --state "$STATE" status iris-mlp
"$MLOP" --state "$STATE" ready iris-mlp --replicas 4
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "scaling to the current count is a no-op"
expect "already at 4 replicas (generation 2)" "$MLOP" --state "$STATE" scale iris-mlp --replicas 4

step "dry-run a scale down: prints the change, writes nothing"
expect "would scale default/iris-mlp from 4 to 1 replicas (generation 2 -> 3)" "$MLOP" --state "$STATE" scale iris-mlp --replicas 1 --dry-run
expect '"generation": 2' "$MLOP" --state "$STATE" status iris-mlp --json
expect '"desired": 4' "$MLOP" --state "$STATE" status iris-mlp --json

step "scale down to 1, then to 0"
expect "scaled default/iris-mlp (generation 3): 4 -> 1 replicas" "$MLOP" --state "$STATE" scale iris-mlp --replicas 1
expect "scaled default/iris-mlp (generation 4): 1 -> 0 replicas" "$MLOP" --state "$STATE" scale iris-mlp --replicas 0
expect "0/0 replicas" "$MLOP" --state "$STATE" status iris-mlp
expect "Scaled" "$MLOP" --state "$STATE" logs iris-mlp

step "scale refuses when autoscaling owns replicas"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-autoscaled.yaml
if "$MLOP" --state "$STATE" scale iris-autoscaled --replicas 3 2>/dev/null; then
  echo "scale should have refused an autoscaled deployment" >&2; exit 1
fi
expect '"desired": 2' "$MLOP" --state "$STATE" status iris-autoscaled --json

step "scale refuses while paused"
"$MLOP" --state "$STATE" scale iris-mlp --replicas 2
"$MLOP" --state "$STATE" pause iris-mlp
if "$MLOP" --state "$STATE" scale iris-mlp --replicas 1 2>/dev/null; then
  echo "scale should have refused a paused deployment" >&2; exit 1
fi
"$MLOP" --state "$STATE" resume iris-mlp
expect "scaled default/iris-mlp (generation 8): 2 -> 1 replicas" "$MLOP" --state "$STATE" scale iris-mlp --replicas 1

echo
echo "tour passed: scale up/down/to-0, no-op, dry-run, autoscaling + paused refusals, Scaled event logged."
