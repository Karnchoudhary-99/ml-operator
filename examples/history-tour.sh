#!/usr/bin/env bash
# mlop history tour: apply v1 -> pods ready -> roll to v2 -> roll
# back to v1, and watch the audit trail grow at every step. Every
# step asserts the expected output, so the script is a demo and a
# smoke test in one. The trail records transitions only: repeated
# status checks on a steady deployment add no entries.
#
# Usage: examples/history-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply iris-mlp v1: history opens with gen 1 Progressing"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" history iris-mlp | grep -q "gen 1 .* Progressing"

step "steady-state status adds no history entries"
"$MLOP" --state "$STATE" status iris-mlp >/dev/null
"$MLOP" --state "$STATE" status iris-mlp >/dev/null
[ "$("$MLOP" --state "$STATE" history iris-mlp | wc -l)" -eq 1 ]

step "pods come up: gen 1 flips to Available"
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
"$MLOP" --state "$STATE" history iris-mlp | grep -q "gen 1 .* Available"
[ "$("$MLOP" --state "$STATE" history iris-mlp | wc -l)" -eq 2 ]

step "roll to v2: gen 2 opens in the trail"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-canary.yaml >/dev/null
"$MLOP" --state "$STATE" history iris-mlp | grep -q "gen 2 .*iris-mlp:v2"

step "promote v2, then roll back to v1 as generation 3"
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2 --canary
sleep 2
"$MLOP" --state "$STATE" rollout status iris-mlp | grep -q "weight 100%"
sleep 2
"$MLOP" --state "$STATE" rollout status iris-mlp | grep -q "state:      promoted"
"$MLOP" --state "$STATE" rollback iris-mlp | grep -q "rolled back default/iris-mlp (generation 3)"
"$MLOP" --state "$STATE" history iris-mlp | grep -q "gen 3 .*iris-mlp:v1"

step "the full trail is JSON too"
"$MLOP" --state "$STATE" history iris-mlp --json | grep -q '"generation": 3'

echo
echo "tour passed: history tracked v1 -> v2 -> rollback to v1 across generations."
