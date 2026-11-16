#!/usr/bin/env bash
# Model-load probe tour: the canary health gate waits for the model
# itself, not just for ready pods. Two scenarios, each on a fresh
# state file:
#
#   A. The model is still loading: the rollout holds at 10% without
#      burning failure budget; once the model loads, it advances
#      and promotes.
#   B. The model fails to load: three failed gates abort the
#      rollout, stable keeps serving v1, and the deployment stays
#      Degraded for that generation.
#
# Usage: examples/model-load-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }
serve_v1() { # $1 = state file
	"$MLOP" --state "$1" apply -f examples/iris-mlp.yaml >/dev/null
	"$MLOP" --state "$1" ready iris-mlp --replicas 2 >/dev/null
	expect "phase:      Available" "$MLOP" --state "$1" status iris-mlp
	"$MLOP" --state "$1" apply -f examples/iris-mlp-canary.yaml >/dev/null
	"$MLOP" --state "$1" ready iris-mlp --replicas 2 --canary >/dev/null
}

step "A. canary v2: pods ready, model still loading"
STATE_A="$WORK/state-a.json"
serve_v1 "$STATE_A"
"$MLOP" --state "$STATE_A" load iris-mlp --canary --loading
# Two reconciles while the model loads: still at 10%, zero failures.
expect "step:       1/2 (weight 10%)" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "failures:   0 consecutive gate failures" "$MLOP" --state "$STATE_A" rollout status iris-mlp

step "A. the model loads: rollout advances and promotes"
"$MLOP" --state "$STATE_A" load iris-mlp --canary
sleep 2
expect "weight 100%" "$MLOP" --state "$STATE_A" rollout status iris-mlp
sleep 2
expect "state:      promoted" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE_A" status iris-mlp
expect "CanaryPromoted" "$MLOP" --state "$STATE_A" logs iris-mlp

step "B. canary v2: pods ready, model fails to load"
STATE_B="$WORK/state-b.json"
serve_v1 "$STATE_B"
expect "phase=Progressing" "$MLOP" --state "$STATE_B" load iris-mlp --canary --fail
# Strikes 2 and 3: the third aborts the rollout.
expect "failures:   2 consecutive gate failures" "$MLOP" --state "$STATE_B" rollout status iris-mlp
expect "state:      aborted" "$MLOP" --state "$STATE_B" rollout status iris-mlp

step "B. stable still serves v1, deployment stays Degraded"
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp
expect "registry.example.com/iris-mlp:v1" "$MLOP" --state "$STATE_B" status iris-mlp
expect "RolloutAborted" "$MLOP" --state "$STATE_B" logs iris-mlp
# And the next reconcile does not flip it back to Available.
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp

echo
echo "tour passed: loading model holds the rollout, loaded model promotes, failed model aborts."
