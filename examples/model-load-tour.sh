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
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }
serve_v1() { # $1 = state file
	"$MLOP" --state "$1" apply -f examples/iris-mlp.yaml >/dev/null
	"$MLOP" --state "$1" ready iris-mlp --replicas 2 >/dev/null
	"$MLOP" --state "$1" status iris-mlp | grep -q "phase:      Available"
	"$MLOP" --state "$1" apply -f examples/iris-mlp-canary.yaml >/dev/null
	"$MLOP" --state "$1" ready iris-mlp --replicas 2 --canary >/dev/null
}

step "A. canary v2: pods ready, model still loading"
STATE_A="$WORK/state-a.json"
serve_v1 "$STATE_A"
"$MLOP" --state "$STATE_A" load iris-mlp --canary --loading
# Two reconciles while the model loads: still at 10%, zero failures.
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "step:       1/2 (weight 10%)"
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "failures:   0 consecutive gate failures"

step "A. the model loads: rollout advances and promotes"
"$MLOP" --state "$STATE_A" load iris-mlp --canary
sleep 2
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "weight 100%"
sleep 2
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "state:      promoted"
"$MLOP" --state "$STATE_A" status iris-mlp | grep -q "registry.example.com/iris-mlp:v2"
"$MLOP" --state "$STATE_A" logs iris-mlp | grep -q "CanaryPromoted"

step "B. canary v2: pods ready, model fails to load"
STATE_B="$WORK/state-b.json"
serve_v1 "$STATE_B"
"$MLOP" --state "$STATE_B" load iris-mlp --canary --fail | grep -q "phase=Progressing"
# Strikes 2 and 3: the third aborts the rollout.
"$MLOP" --state "$STATE_B" rollout status iris-mlp | grep -q "failures:   2 consecutive gate failures"
"$MLOP" --state "$STATE_B" rollout status iris-mlp | grep -q "state:      aborted"

step "B. stable still serves v1, deployment stays Degraded"
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "phase:      Degraded"
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "registry.example.com/iris-mlp:v1"
"$MLOP" --state "$STATE_B" logs iris-mlp | grep -q "RolloutAborted"
# And the next reconcile does not flip it back to Available.
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "phase:      Degraded"

echo
echo "tour passed: loading model holds the rollout, loaded model promotes, failed model aborts."
