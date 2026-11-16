#!/usr/bin/env bash
# mlop security tour: a plain manifest is hardened by default ->
# status shows the effective security context -> an explicit
# security block changes the uid and the dropped capabilities ->
# diff previews a security change -> a relaxed manifest warns on
# validate and shows up in status -> a contradictory manifest
# (uid 0 with runAsNonRoot on) is rejected at apply time.
# Every step asserts the expected output, so the script is a demo
# and a smoke test in one.
#
# Usage: examples/security-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply the plain manifest: security is hardened by default"
expect "CreateDeployment" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
expect "security:     runAsNonRoot on, uid 10001, read-only rootfs on, privilege escalation off, drop ALL, seccomp RuntimeDefault" "$MLOP" --state "$STATE" status iris-mlp
expect '"RunAsUser": 10001' "$MLOP" --state "$STATE" status iris-mlp --json

step "the secured manifest validates clean: explicit hardening is not a relaxation"
expect "valid: default/iris-mlp" "$MLOP" --state "$STATE" validate -f examples/iris-mlp-secured.yaml
expect "no warnings" "$MLOP" --state "$STATE" validate -f examples/iris-mlp-secured.yaml

step "diff previews the security change without applying it"
expect "runAsUser 10001 -> 20000" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-secured.yaml
expect "dropCapabilities changed" "$MLOP" --state "$STATE" diff -f examples/iris-mlp-secured.yaml
expect "security:     runAsNonRoot on, uid 10001" "$MLOP" --state "$STATE" status iris-mlp

step "apply the secured manifest: uid and capabilities change"
expect "UpdateDeployment" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-secured.yaml
expect "security:     runAsNonRoot on, uid 20000, read-only rootfs on, privilege escalation off, drop NET_BIND_SERVICE, seccomp RuntimeDefault" "$MLOP" --state "$STATE" status iris-mlp

step "re-applying the same manifest is a no-op: security is compared by value"
expect "unchanged default/iris-mlp (generation 2)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp-secured.yaml

step "a relaxed manifest warns on validate and shows up in status"
sed 's/readOnlyRootFilesystem: true/readOnlyRootFilesystem: false/' examples/iris-mlp-secured.yaml > "$WORK/relaxed.yaml"
expect "warning: spec.security.readOnlyRootFilesystem is false" "$MLOP" --state "$STATE" validate -f "$WORK/relaxed.yaml"
expect "UpdateDeployment" "$MLOP" --state "$STATE" apply -f "$WORK/relaxed.yaml"
expect "security:     runAsNonRoot on, uid 20000, read-only rootfs off" "$MLOP" --state "$STATE" status iris-mlp

step "a contradictory manifest is rejected at apply time"
sed 's/runAsUser: 20000/runAsUser: 0/' examples/iris-mlp-secured.yaml > "$WORK/blocked.yaml"
if "$MLOP" --state "$STATE" apply -f "$WORK/blocked.yaml" 2>"$WORK/apply-err.txt"; then
  echo "expected uid 0 with runAsNonRoot on to be rejected" >&2; exit 1
fi
grep -q "runAsUser is 0 but runAsNonRoot is on" "$WORK/apply-err.txt"

echo
echo "tour passed: security hardens by default, explicit blocks render, relaxations warn, contradictions are rejected."
