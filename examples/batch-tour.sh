#!/usr/bin/env bash
# mlop batch tour: dry-run a two-model directory, apply it, bring
# both models up, and wait for both with a single `mlop watch
# --all`. Every step asserts the expected output, so the script is
# a demo and a smoke test in one.
#
# Usage: examples/batch-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "dry-run the multi-model directory: both manifests valid, no state touched"
expect "summary: 2 valid, 0 failed" "$MLOP" --state "$STATE" apply -d examples/multi-model --dry-run
[ ! -e "$STATE" ]

step "apply the directory: iris first (a-...), house-price second (b-...)"
expect "summary: 2 applied, 0 unchanged, 0 failed" "$MLOP" --state "$STATE" apply -d examples/multi-model
expect "iris-mlp" "$MLOP" --state "$STATE" status
expect "house-price" "$MLOP" --state "$STATE" status

step "a second apply changes nothing"
expect "summary: 0 applied, 2 unchanged, 0 failed" "$MLOP" --state "$STATE" apply -d examples/multi-model

step "bring both models up and wait for both at once"
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
"$MLOP" --state "$STATE" ready house-price --replicas 1
"$MLOP" --state "$STATE" watch --all --interval 100ms --timeout 5s

echo
echo "tour passed: batch dry-run -> apply -> re-apply -> watch --all, both models Available."
