#!/usr/bin/env bash
# Blue-green tour: v2 ships as a full-size green Deployment beside
# the stable v1 one, and the Service flips to green only once the
# health gate passes. Traffic never splits. Two scenarios, each on a
# fresh state file:
#
#   A. Green soaks: the model is still loading, so the rollout holds
#      without burning failure budget; once the model loads and the
#      pods are ready, the Service switches to green and the next
#      reconcile folds green into stable.
#   B. The model fails to load: three failed gates abort the
#      rollout, green is deleted, stable keeps serving v1, and the
#      deployment stays Degraded for that generation.
#
# Usage: examples/bluegreen-tour.sh
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
	"$MLOP" --state "$1" apply -f examples/iris-mlp-bluegreen.yaml >/dev/null
}

step "A. blue-green v2: green soaks beside stable v1"
STATE_A="$WORK/state-a.json"
serve_v1 "$STATE_A"
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "state:      soaking"
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "failures:   0 consecutive gate failures"
# A diff before the cutover previews nothing left to do: green
# already exists and stable is pinned to v1.
"$MLOP" --state "$STATE_A" diff iris-mlp | grep -q "in sync"

step "A. the model is still loading: the rollout holds, no budget burned"
"$MLOP" --state "$STATE_A" load iris-mlp --green --loading
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "state:      soaking"
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "failures:   0 consecutive gate failures"

step "A. the model loads and the pods are ready: the Service switches to green"
"$MLOP" --state "$STATE_A" load iris-mlp --green
"$MLOP" --state "$STATE_A" ready iris-mlp --replicas 2 --green | grep -q "phase=Progressing"
"$MLOP" --state "$STATE_A" logs iris-mlp | grep -q "BlueGreenSwitched"

step "A. the next reconcile folds green into stable"
"$MLOP" --state "$STATE_A" rollout status iris-mlp | grep -q "state:      folded"
"$MLOP" --state "$STATE_A" status iris-mlp | grep -q "registry.example.com/iris-mlp:v2"
"$MLOP" --state "$STATE_A" status iris-mlp | grep -q "previous:   registry.example.com/iris-mlp:v1"
"$MLOP" --state "$STATE_A" logs iris-mlp | grep -q "BlueGreenPromoted"

step "B. blue-green v2: the model fails to load"
STATE_B="$WORK/state-b.json"
serve_v1 "$STATE_B"
"$MLOP" --state "$STATE_B" load iris-mlp --green --fail
# Green pods are not ready yet, so the failed load cannot burn
# budget until there is something to judge.
"$MLOP" --state "$STATE_B" rollout status iris-mlp | grep -q "failures:   0 consecutive gate failures"
"$MLOP" --state "$STATE_B" ready iris-mlp --replicas 2 --green | grep -q "phase=Progressing"
# Strikes 2 and 3: the third aborts the rollout.
"$MLOP" --state "$STATE_B" rollout status iris-mlp | grep -q "failures:   2 consecutive gate failures"
"$MLOP" --state "$STATE_B" rollout status iris-mlp | grep -q "state:      aborted"

step "B. stable still serves v1, deployment stays Degraded"
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "phase:      Degraded"
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "registry.example.com/iris-mlp:v1"
"$MLOP" --state "$STATE_B" logs iris-mlp | grep -q "BlueGreenAborted"
# And the next reconcile does not flip it back to Available.
"$MLOP" --state "$STATE_B" status iris-mlp | grep -q "phase:      Degraded"

echo
echo "tour passed: green soaks, switches, and folds on a healthy gate; a failed model aborts and blue keeps serving."
