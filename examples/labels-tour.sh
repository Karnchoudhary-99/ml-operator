#!/usr/bin/env bash
# mlop labels-and-annotations tour: a plain manifest carries no
# metadata -> status shows neither line -> the labelled manifest
# stamps labels and annotations onto every rendered object ->
# status shows them (text + JSON) -> diff previews a label change
# -> re-applying is a no-op (maps compare by content) -> drift
# reports the labelled deployment in sync -> a reserved-prefix
# label is rejected at apply time -> dropping the metadata
# removes both lines again.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/labels-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the plain manifest: no custom metadata"
expect "applied default/iris-mlp (generation 1)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "labels:"; then
  echo "metadata-less deployment should show no labels line" >&2; exit 1
fi
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "annotations:"; then
  echo "metadata-less deployment should show no annotations line" >&2; exit 1
fi

step "diff previews the metadata change without applying it"
expect "labels changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-labelled.yaml
expect "annotations changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-labelled.yaml

step "apply the labelled manifest: every object carries the metadata"
expect "applied default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-labelled.yaml
expect "labels:       cost-center=ml-platform, team=search" "$MLOP" --state "$STATE" status iris-mlp
expect "annotations:  owner=team-search, runbook=https://example.com/runbooks/iris-mlp" "$MLOP" --state "$STATE" status iris-mlp
expect '"team": "search"' "$MLOP" --state "$STATE" status iris-mlp --json
expect '"owner": "team-search"' "$MLOP" --state "$STATE" status iris-mlp --json

step "re-applying the same manifest is a no-op: maps compare by content"
expect "unchanged default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-labelled.yaml

step "drift reports the labelled deployment in sync"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

step "a reserved-prefix label is rejected at apply time"
sed 's/team: search/team: search\n    ml-operator\/team: search/' examples/iris-mlp-labelled.yaml > "$WORK/reserved.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/reserved.yaml" 2>"$WORK/apply-err.txt"; then
  echo "expected a reserved-prefix label to be rejected" >&2; exit 1
fi
grep -q "reserved prefix" "$WORK/apply-err.txt"

step "dropping the metadata removes both lines again"
expect "applied default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "labels:"; then
  echo "metadata-less deployment should show no labels line" >&2; exit 1
fi
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "annotations:"; then
  echo "metadata-less deployment should show no annotations line" >&2; exit 1
fi

echo
echo "tour passed: labels and annotations stamp onto every rendered object, diff names a change, drift stays in sync, reserved prefixes are rejected."
