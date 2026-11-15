#!/usr/bin/env bash
# mlop backup tour: apply v1 -> pods ready -> snapshot the world ->
# wipe the state file -> restore it and watch everything come back,
# then prove a tampered backup is refused. Every step asserts the
# expected output, so the script is a demo and a smoke test in one.
#
# Usage: examples/backup-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"
BACKUP="$WORK/iris-backup.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

step "apply iris-mlp v1 and bring its pods up"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
"$MLOP" --state "$STATE" status iris-mlp | grep -q "Available"

step "snapshot the world: one deployment, checksum printed"
"$MLOP" --state "$STATE" backup "$BACKUP" | grep -q "backed up 1 deployment(s)"
step "a second backup to the same file is refused without --force"
if "$MLOP" --state "$STATE" backup "$BACKUP" 2>/tmp/backup-err.txt; then
  echo "expected the second backup to fail" >&2; exit 1
fi
grep -q "already exists" /tmp/backup-err.txt

step "wipe the state file: the world is gone"
rm "$STATE"
"$MLOP" --state "$STATE" status | grep -q "no deployments"

step "restore: the deployment, its readiness and its history return"
"$MLOP" --state "$STATE" restore "$BACKUP" | grep -q "restored 1 deployment(s)"
"$MLOP" --state "$STATE" status iris-mlp | grep -q "Available"
"$MLOP" --state "$STATE" history iris-mlp | grep -q "Available"

step "a tampered backup is refused and leaves no state behind"
sed 's/iris-mlp:v1/iris-mlp:v2/' "$BACKUP" > "$WORK/tampered.json"
rm "$STATE"
if "$MLOP" --state "$STATE" restore "$WORK/tampered.json" 2>/tmp/restore-err.txt; then
  echo "expected the tampered restore to fail" >&2; exit 1
fi
grep -q "checksum mismatch" /tmp/restore-err.txt
[ ! -e "$STATE" ]

echo
echo "tour passed: backup -> wipe -> restore brought the world back, tampering was caught."
