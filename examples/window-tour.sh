#!/usr/bin/env bash
# mlop maintenance-window tour: apply v1 inside an open window and
# bring it Available, then roll to v2 under a window that opens
# only tomorrow — the apply registers but the cluster keeps serving
# v1, status names the wait and the next opening — then re-apply
# v2 under a window open today and watch it land. A short window
# also draws the `mlop validate` warning. Driven entirely through
# the CLI; every step asserts the expected output, so the script
# is a demo and a smoke test in one.
#
# The days are computed from the UTC clock, never hardcoded: the
# closed window opens on tomorrow's weekday, the open window on
# today's, so the tour is deterministic whatever day it runs.
#
# Usage: examples/window-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

# date +%u is 1..7 (Mon..Sun); the array is 0-based, so today is
# index num-1 and tomorrow wraps with % 7.
DAYS=(Mon Tue Wed Thu Fri Sat Sun)
TODAY_NUM=$(date -u +%u)
TODAY=${DAYS[$((TODAY_NUM - 1))]}
TOMORROW=${DAYS[$((TODAY_NUM % 7))]}

# window <file> <day> <start> <end>: copy iris-mlp.yaml and append
# a maintenanceWindow block (the parser does not care about key
# order, so replicas is re-emitted with the new block).
window() {
	{ sed '/^  replicas: 2$/d' examples/iris-mlp.yaml
	  printf '  replicas: 2\n  maintenanceWindow:\n    days: %s\n    start: %s\n    end: %s\n' "$2" "$3" "$4"
	} > "$1"
}
# v2 <file>: the same manifest, serving v2 instead of v1.
v2() { sed 's/iris-mlp:v1/iris-mlp:v2/; s/version: v1/version: v2/' "$1" > "$1.v2"; }

step "apply iris-mlp v1 inside an open window (today, all day)"
window "$WORK/v1-open.yaml" "$TODAY" "00:00" "23:59"
"$MLOP" --state "$STATE" apply -f "$WORK/v1-open.yaml"
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp
expect "window:     $TODAY 00:00–23:59 UTC (open now)" "$MLOP" --state "$STATE" status iris-mlp

step "roll to v2 under a window that opens only tomorrow: it waits"
window "$WORK/v2-closed.yaml" "$TOMORROW" "02:00" "04:00"
v2 "$WORK/v2-closed.yaml"
expect "waiting for maintenance window" "$MLOP" --state "$STATE" apply -f "$WORK/v2-closed.yaml.v2"
expect "next opens $TOMORROW 02:00 UTC" "$MLOP" --state "$STATE" status iris-mlp
# status shows the desired image (v2) — that is what the manifest
# registered. That the cluster still serves v1 is proven by diff:
# the v2 plan is still pending, not applied.
expect "UpdateDeployment default/iris-mlp" "$MLOP" --state "$STATE" diff iris-mlp
expect "outside its maintenance window" "$MLOP" --state "$STATE" diff iris-mlp
expect "WindowBlocked: True (WindowBlocked)" "$MLOP" --state "$STATE" status iris-mlp
expect "window:     $TOMORROW 02:00–04:00 UTC (closed — next opens $TOMORROW 02:00 UTC)" "$MLOP" --state "$STATE" status iris-mlp

step "the same v2 under a window open today applies at once"
window "$WORK/v2-open.yaml" "$TODAY" "00:00" "23:59"
v2 "$WORK/v2-open.yaml"
expect "applied default/iris-mlp (generation 3)" "$MLOP" --state "$STATE" apply -f "$WORK/v2-open.yaml.v2"
expect "model:      registry.example.com/iris-mlp:v2 (onnx, v2)" "$MLOP" --state "$STATE" status iris-mlp
expect "WindowBlocked: False (WindowOpen)" "$MLOP" --state "$STATE" status iris-mlp
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "a 10-minute window draws the validate warning"
window "$WORK/v1-short.yaml" "$TODAY" "02:00" "02:10"
expect "only 10 minutes long" "$MLOP" validate -f "$WORK/v1-short.yaml"

step "an invalid window is rejected at apply time"
window "$WORK/v1-bad.yaml" "$TODAY" "02:00" "02:00"
if "$MLOP" --state "$STATE" apply -f "$WORK/v1-bad.yaml" 2>/dev/null; then
  echo "apply should have rejected start == end" >&2; exit 1
fi

echo
echo "tour passed: open-window apply, closed-window deferral with next opening, re-apply on window open, short-window warning, invalid window rejected."
