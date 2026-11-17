#!/usr/bin/env bash
# mlop model-source tour: a plain manifest serves weights from
# the image -> status shows no modelSource line -> the sourced
# manifest downloads the artifact at startup -> status names
# the source and its verification -> diff previews a source
# change -> re-applying is a no-op (the source compares by
# value) -> drift reports the sourced deployment in sync -> an
# unverified download warns on validate -> an s3:// URI is
# rejected at apply time -> dropping the source serves from
# the image again.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/model-source-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the plain manifest: weights come from the image, no source"
expect "applied default/iris-mlp (generation 1)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "modelSource:"; then
  echo "unsourced deployment should show no modelSource line" >&2; exit 1
fi

step "diff previews the source change without applying it"
expect "model download init container changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-sourced.yaml
expect "model artifact volume changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-sourced.yaml
expect "model artifact mount changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-sourced.yaml

step "apply the sourced manifest: the artifact downloads at startup"
expect "applied default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-sourced.yaml
expect "modelSource:  https://artifacts.example.com/models/iris-mlp.onnx -> /models/iris-mlp.onnx (sha256: 9f2c4a1b8e77… verified)" "$MLOP" --state "$STATE" status iris-mlp
expect '"URI": "https://artifacts.example.com/models/iris-mlp.onnx"' "$MLOP" --state "$STATE" status iris-mlp --json
expect '"SHA256": "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807"' "$MLOP" --state "$STATE" status iris-mlp --json

step "re-applying the same manifest is a no-op: the source is compared by value"
expect "unchanged default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-sourced.yaml

step "drift reports the sourced deployment in sync"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

step "an unverified download warns on validate"
sed 's/    sha256: .*//' examples/iris-mlp-sourced.yaml > "$WORK/unverified.yaml"
expect "warning: spec.modelSource.sha256 is empty" "$MLOP" validate -f "$WORK/unverified.yaml"
expect "without checksum verification" "$MLOP" validate -f "$WORK/unverified.yaml"

step "an s3:// URI is rejected at apply time"
sed 's|uri: https://artifacts.example.com/models/iris-mlp.onnx|uri: s3://artifacts/iris-mlp.onnx|' examples/iris-mlp-sourced.yaml > "$WORK/s3.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/s3.yaml" 2>"$WORK/apply-err.txt"; then
  echo "expected an s3:// URI to be rejected" >&2; exit 1
fi
grep -q "must be an http(s) URL" "$WORK/apply-err.txt"

step "dropping the source serves from the image again"
expect "applied default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "modelSource:"; then
  echo "unsourced deployment should show no modelSource line" >&2; exit 1
fi

echo
echo "tour passed: weights in the image by default, a source downloads and verifies the artifact at startup, diff names the change, drift stays in sync, unverified downloads warn, s3:// URIs are rejected."
