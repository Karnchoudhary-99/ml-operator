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
	"$MLOP" --state "$1" apply -f examples/iris-mlp-bluegreen.yaml >/dev/null
}

step "A. blue-green v2: green soaks beside stable v1"
STATE_A="$WORK/state-a.json"
serve_v1 "$STATE_A"
expect "state:      soaking" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "failures:   0 consecutive gate failures" "$MLOP" --state "$STATE_A" rollout status iris-mlp
# A diff before the cutover previews nothing left to do: green
# already exists and stable is pinned to v1.
expect "in sync" "$MLOP" --state "$STATE_A" diff iris-mlp

step "A. the model is still loading: the rollout holds, no budget burned"
"$MLOP" --state "$STATE_A" load iris-mlp --green --loading
expect "state:      soaking" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "failures:   0 consecutive gate failures" "$MLOP" --state "$STATE_A" rollout status iris-mlp

step "A. the model loads and the pods are ready: the Service switches to green"
"$MLOP" --state "$STATE_A" load iris-mlp --green
expect "phase=Progressing" "$MLOP" --state "$STATE_A" ready iris-mlp --replicas 2 --green
expect "BlueGreenSwitched" "$MLOP" --state "$STATE_A" logs iris-mlp

step "A. the next reconcile folds green into stable"
expect "state:      folded" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE_A" status iris-mlp
expect "previous:   registry.example.com/iris-mlp:v1" "$MLOP" --state "$STATE_A" status iris-mlp
expect "BlueGreenPromoted" "$MLOP" --state "$STATE_A" logs iris-mlp

step "B. blue-green v2: the model fails to load"
STATE_B="$WORK/state-b.json"
serve_v1 "$STATE_B"
"$MLOP" --state "$STATE_B" load iris-mlp --green --fail
# Green pods are not ready yet, so the failed load cannot burn
# budget until there is something to judge.
expect "failures:   0 consecutive gate failures" "$MLOP" --state "$STATE_B" rollout status iris-mlp
expect "phase=Progressing" "$MLOP" --state "$STATE_B" ready iris-mlp --replicas 2 --green
# Strikes 2 and 3: the third aborts the rollout.
expect "failures:   2 consecutive gate failures" "$MLOP" --state "$STATE_B" rollout status iris-mlp
expect "state:      aborted" "$MLOP" --state "$STATE_B" rollout status iris-mlp

step "B. stable still serves v1, deployment stays Degraded"
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp
expect "registry.example.com/iris-mlp:v1" "$MLOP" --state "$STATE_B" status iris-mlp
expect "BlueGreenAborted" "$MLOP" --state "$STATE_B" logs iris-mlp
# And the next reconcile does not flip it back to Available.
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp

echo
echo "tour passed: green soaks, switches, and folds on a healthy gate; a failed model aborts and blue keeps serving."
