#!/usr/bin/env bash
# Traffic simulation tour: the canary health gate finally judges
# real traffic signals instead of staying readiness-only. Two
# scenarios, each on a fresh state file:
#
#   A. Clean traffic (0.5% errors, p95 180ms against the canary
#      manifest's 2% / 250ms gate): the rollout advances and
#      promotes, and `mlop status` shows the observation.
#   B. Bad traffic (20% errors against the same gate): three failed
#      gates abort the rollout, stable keeps serving v1, and the
#      deployment stays Degraded for that generation.
#
# Usage: examples/traffic-tour.sh
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

step "A. canary v2: clean traffic, gate passes"
STATE_A="$WORK/state-a.json"
serve_v1 "$STATE_A"
expect "traffic 1000 requests, 5 errors (0.50% error rate, p95 180ms)" "$MLOP" --state "$STATE_A" traffic iris-mlp --canary --requests 1000 --errors 5 --p95-ms 180
expect "traffic canary: 1000 requests, 5 errors (0.50% error rate), p95 180ms" "$MLOP" --state "$STATE_A" status iris-mlp
expect "failures:   0 consecutive gate failures" "$MLOP" --state "$STATE_A" rollout status iris-mlp

step "A. the rollout advances and promotes"
sleep 2
expect "weight 100%" "$MLOP" --state "$STATE_A" rollout status iris-mlp
sleep 2
expect "state:      promoted" "$MLOP" --state "$STATE_A" rollout status iris-mlp
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE_A" status iris-mlp
expect "CanaryPromoted" "$MLOP" --state "$STATE_A" logs iris-mlp

step "B. canary v2: bad traffic, gate fails"
STATE_B="$WORK/state-b.json"
serve_v1 "$STATE_B"
expect "traffic 100 requests, 20 errors (20.00% error rate, p95 180ms)" "$MLOP" --state "$STATE_B" traffic iris-mlp --canary --requests 100 --errors 20 --p95-ms 180
# Strikes 2 and 3: the third aborts the rollout. The error rate
# stays 20% — the observation persists across reconciles.
expect "failures:   2 consecutive gate failures" "$MLOP" --state "$STATE_B" rollout status iris-mlp
expect "state:      aborted" "$MLOP" --state "$STATE_B" rollout status iris-mlp

step "B. stable still serves v1, deployment stays Degraded"
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp
expect "registry.example.com/iris-mlp:v1" "$MLOP" --state "$STATE_B" status iris-mlp
expect "RolloutAborted" "$MLOP" --state "$STATE_B" logs iris-mlp
# And the next reconcile does not flip it back to Available.
expect "phase:      Degraded" "$MLOP" --state "$STATE_B" status iris-mlp

echo
echo "tour passed: clean traffic promotes the canary, bad traffic aborts it."
