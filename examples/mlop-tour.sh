#!/usr/bin/env bash
# mlop end-to-end tour: apply v1 -> pods ready -> Available, then a
# canary rollout of v2 (10% -> 100% -> promote) driven entirely
# through the CLI. Every step asserts the expected output, so the
# script is a demo and a smoke test in one.
#
# Usage: examples/mlop-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "validate the manifests before applying anything"
expect "valid: default/iris-mlp" "$MLOP" --state "$STATE" validate -f examples/iris-mlp.yaml
expect "valid: default/iris-mlp" "$MLOP" --state "$STATE" validate -f examples/iris-mlp-canary.yaml --strict

step "apply iris-mlp v1"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml

step "status (Progressing, 0/2 ready)"
"$MLOP" --state "$STATE" status iris-mlp

step "pods come up"
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "canary rollout of v2 (10% -> 100% -> promote)"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-canary.yaml
"$MLOP" --state "$STATE" rollout status iris-mlp
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2 --canary
sleep 2
expect "weight 100%" "$MLOP" --state "$STATE" rollout status iris-mlp
sleep 2
expect "state:      promoted" "$MLOP" --state "$STATE" rollout status iris-mlp

step "diff previews the next change, and changes nothing"
# v2 is live: diffing the v2 manifest is a no-op, and diffing the v1
# manifest previews the way back — without applying either.
expect "in sync" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-canary.yaml
expect "UpdateDeployment default/iris-mlp" "$MLOP" --state "$STATE" diff -f examples/iris-mlp.yaml
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE" status iris-mlp

step "v2 is live, and the promotion is in the log"
expect "registry.example.com/iris-mlp:v2" "$MLOP" --state "$STATE" status iris-mlp
expect "CanaryPromoted" "$MLOP" --state "$STATE" logs iris-mlp

echo
echo "tour passed: v1 applied, v2 canaried and promoted, events logged."
