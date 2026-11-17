#!/usr/bin/env bash
# mlop image-pull-secrets tour: a plain manifest pulls without
# credentials -> status shows no imagePullSecrets line -> the
# secreted manifest names the pull secrets -> status shows
# them (text + JSON) -> diff previews a rotation -> re-applying
# is a no-op (the list compares by value) -> drift reports the
# secreted deployment in sync -> a duplicate secret is
# rejected at apply time -> dropping the secrets pulls without
# credentials again.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/image-pull-secrets-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the plain manifest: pulls without credentials"
expect "applied default/iris-mlp (generation 1)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "imagePullSecrets:"; then
  echo "secretless deployment should show no imagePullSecrets line" >&2; exit 1
fi

step "diff previews the secrets change without applying it"
expect "imagePullSecrets changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-pull-secrets.yaml

step "apply the secreted manifest: the pods pull with credentials"
expect "applied default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-pull-secrets.yaml
expect "imagePullSecrets: regcred, ghcr-creds" "$MLOP" --state "$STATE" status iris-mlp
expect '"regcred"' "$MLOP" --state "$STATE" status iris-mlp --json
expect '"ghcr-creds"' "$MLOP" --state "$STATE" status iris-mlp --json

step "re-applying the same manifest is a no-op: the list is compared by value"
expect "unchanged default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-pull-secrets.yaml

step "drift reports the secreted deployment in sync"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

step "a duplicate secret is rejected at apply time"
sed 's/imagePullSecrets: regcred, ghcr-creds/imagePullSecrets: regcred, regcred/' examples/iris-mlp-pull-secrets.yaml > "$WORK/dup.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/dup.yaml" 2>"$WORK/apply-err.txt"; then
  echo "expected a duplicate secret to be rejected" >&2; exit 1
fi
grep -q "duplicates secret" "$WORK/apply-err.txt"

step "dropping the secrets pulls without credentials again"
expect "applied default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
if "$MLOP" --state "$STATE" status iris-mlp | grep -q "imagePullSecrets:"; then
  echo "secretless deployment should show no imagePullSecrets line" >&2; exit 1
fi

echo
echo "tour passed: pulls without credentials by default, secrets name the registry credentials, diff names a rotation, drift stays in sync, duplicates are rejected."
