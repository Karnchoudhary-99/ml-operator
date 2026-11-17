#!/usr/bin/env bash
# mlop placement tour: a plain manifest schedules anywhere -> status
# shows no placement line -> the placed manifest pins the model to
# GPU nodes (nodeSelector + tolerations) -> diff previews a
# placement change -> re-applying is a no-op (placement compares by
# value) -> drift reports the placed deployment in sync -> a broad
# toleration warns on validate -> a contradictory placement is
# rejected at apply time -> dropping the placement unpins the model.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/placement-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the plain manifest: no placement, pods schedule anywhere"
expect "applied default/iris-mlp (generation 1)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "placement:"; then
  echo "unconstrained deployment should show no placement line" >&2; exit 1
fi

step "diff previews the placement change without applying it"
expect "nodeSelector changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-placed.yaml
expect "tolerations changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-placed.yaml

step "apply the placed manifest: GPU nodes only"
expect "applied default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-placed.yaml
expect "placement:    nodeSelector accelerator=nvidia-tesla-t4, tolerations gpu=true:NoSchedule, dedicated:Exists:NoExecute:300" "$MLOP" --state "$STATE" status iris-mlp
expect '"accelerator": "nvidia-tesla-t4"' "$MLOP" --state "$STATE" status iris-mlp --json
expect '"TolerationSeconds": 300' "$MLOP" --state "$STATE" status iris-mlp --json

step "re-applying the same manifest is a no-op: placement is compared by value"
expect "unchanged default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-placed.yaml

step "drift reports the placed deployment in sync"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

step "a broad toleration warns on validate"
sed 's/tolerations: .*/tolerations: :Exists:NoSchedule/' examples/iris-mlp-placed.yaml > "$WORK/broad.yaml"
expect "warning: spec.placement.tolerations has an Exists toleration with an empty key" "$MLOP" validate -f "$WORK/broad.yaml"
expect "tolerates every taint" "$MLOP" validate -f "$WORK/broad.yaml"

step "a NoExecute toleration without a grace period warns too"
sed 's/tolerations: .*/tolerations: dedicated:Exists:NoExecute/' examples/iris-mlp-placed.yaml > "$WORK/nograce.yaml"
expect "evicted immediately" "$MLOP" validate -f "$WORK/nograce.yaml"

step "a contradictory placement is rejected at apply time"
sed 's/tolerations: .*/tolerations: gpu=true:NoSchedule:300/' examples/iris-mlp-placed.yaml > "$WORK/blocked.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/blocked.yaml" 2>"$WORK/apply-err.txt"; then
  echo "expected tolerationSeconds without NoExecute to be rejected" >&2; exit 1
fi
grep -q "only meaningful with effect" "$WORK/apply-err.txt"

step "dropping the placement unpins the model"
expect "applied default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "placement:"; then
  echo "unconstrained deployment should show no placement line" >&2; exit 1
fi

echo
echo "tour passed: unconstrained by default, placement pins to GPU nodes, diff names the change, drift stays in sync, broad tolerations warn, contradictions are rejected."
