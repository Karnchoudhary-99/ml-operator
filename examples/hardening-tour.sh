#!/usr/bin/env bash
# mlop hardening tour: apply a 3-replica deployment with an explicit
# availability block -> status shows the effective values -> scale
# down to one replica and watch the PDB go away (nothing left to
# protect) -> scale back up and watch it return. Every step asserts
# the expected output, so the script is a demo and a smoke test in one.
#
# Usage: examples/hardening-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the hardened manifest: 3 replicas, minAvailable 2"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-hardened.yaml | grep -q "CreatePDB"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "availability: minAvailable 2/3, anti-affinity on, maxSkew 1"
"$MLOP" --state "$STATE" status iris-mlp --json | grep -q '"MinAvailable": 2'

step "diff previews a minAvailable change without applying it"
sed 's/minAvailable: 2/minAvailable: 1/' examples/iris-mlp-hardened.yaml > "$WORK/relaxed.yaml"
"$MLOP" --state "$STATE" diff -f "$WORK/relaxed.yaml" | grep -q "minAvailable 2 -> 1"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "availability: minAvailable 2/3"

step "scale down to one replica: the PDB goes away with it"
# minAvailable 0 means the default, which resolves to 0 for a
# single replica — the deployment has nothing left to protect.
sed -e 's/replicas: 3/replicas: 1/' -e 's/minAvailable: 2/minAvailable: 0/' examples/iris-mlp-hardened.yaml > "$WORK/single.yaml"
"$MLOP" --state "$STATE" apply -f "$WORK/single.yaml" | grep -q "DeletePDB"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "availability: minAvailable 0/1, anti-affinity on, maxSkew 1"

step "scale back up: the PDB returns"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp-hardened.yaml | grep -q "CreatePDB"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "availability: minAvailable 2/3"

step "an invalid availability block is rejected at apply time"
sed 's/minAvailable: 2/minAvailable: 3/' examples/iris-mlp-hardened.yaml > "$WORK/blocked.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/blocked.yaml" 2>/tmp/apply-err.txt; then
  echo "expected minAvailable == replicas to be rejected" >&2; exit 1
fi
grep -q "must be < replicas" /tmp/apply-err.txt

echo
echo "tour passed: PDB follows the replica count, diff previews it, invalid blocks are rejected."
