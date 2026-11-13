#!/usr/bin/env bash
# mlop rollback tour: apply v1 -> canary v2 and promote it -> roll
# back to v1, driven entirely through the CLI. Every step asserts
# the expected output, so the script is a demo and a smoke test in
# one. The rollback is a new generation of the same deployment, so
# it goes through the deployment's normal strategy; here the v1
# rollback starts a fresh canary, which the tour leaves in flight
# (promoting it is the same canary flow the mlop tour already
# covers).
#
# Usage: examples/rollback-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply iris-mlp v1"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
"$MLOP" --state "$STATE" status iris-mlp | grep -q "phase:      Available"

step "rollback refuses: nothing has replaced v1 yet"
if "$MLOP" --state "$STATE" rollback iris-mlp 2>/dev/null; then
  echo "rollback should have refused" >&2; exit 1
fi

step "canary rollout of v2 (10% -> 100% -> promote)"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-canary.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2 --canary
sleep 2
"$MLOP" --state "$STATE" rollout status iris-mlp | grep -q "weight 100%"
sleep 2
"$MLOP" --state "$STATE" rollout status iris-mlp | grep -q "state:      promoted"

step "v2 is live and v1 is the rollback target"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "registry.example.com/iris-mlp:v2"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "previous:   registry.example.com/iris-mlp:v1 (v1)"

step "roll back to v1"
"$MLOP" --state "$STATE" rollback iris-mlp | grep -q "rolled back default/iris-mlp (generation 3)"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "registry.example.com/iris-mlp:v1"
"$MLOP" --state "$STATE" logs iris-mlp | grep -q "RolledBack"

echo
echo "tour passed: v2 promoted, rollback restored v1 as generation 3, event logged."
