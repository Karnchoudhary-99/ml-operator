#!/usr/bin/env bash
# mlop drift tour: apply v1 and bring it Available, then simulate
# out-of-band edits to the live objects (the way a kubectl edit or
# a manual scale would) and watch drift name exactly what moved —
# then re-apply and watch the reconciler put the world back. Every
# step asserts the expected output, so the script is a demo and a
# smoke test in one.
#
# Usage: examples/drift-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop

step() { echo; echo "=== $* ==="; }

# expect_exit <code> <pattern> <command...>
# Like expect, but the command must exit with <code> (drift exits
# 1 when drift is found, which is the success case here).
expect_exit() {
	local want="$1"; local pat="$2"; shift 2
	local out rc
	out="$("$@")" && rc=0 || rc=$?
	if [ "$rc" -ne "$want" ]; then
		echo "expected exit $want, but $* exited $rc" >&2
		exit 1
	fi
	if printf '%s\n' "$out" | grep -q -- "$pat"; then
		return 0
	fi
	echo "expected $pat in output of $* , got:" >&2
	echo "$out" >&2
	exit 1
}

# tamper_state <python...>: rewrite the persisted cluster JSON the
# way an out-of-band edit would — the registered spec is untouched,
# only the live objects move.
tamper_state() {
	python3 - "$STATE" <<'PYEOF'
import json, sys
state = json.load(open(sys.argv[1]))
cluster = state["cluster"]
dep = cluster["Deployments"]["default/iris-mlp"]
dep["Container"]["Image"] = "registry.example.com/iris-mlp:v99"
del cluster["Services"]["default/iris-mlp"]
json.dump(state, open(sys.argv[1], "w"), indent=2)
PYEOF
}

step "apply iris-mlp v1 and bring it Available"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "a clean world reports no drift"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift --all

step "out-of-band edits: image swapped, Service deleted"
tamper_state

step "drift names exactly what moved, and exits 1"
expect_exit 1 "Deployment default/iris-mlp: drifted" "$MLOP" --state "$STATE" drift iris-mlp
expect_exit 1 'image "registry.example.com/iris-mlp:v99" -> "registry.example.com/iris-mlp:v1"' "$MLOP" --state "$STATE" drift iris-mlp
expect_exit 1 "Service default/iris-mlp: missing" "$MLOP" --state "$STATE" drift iris-mlp
expect_exit 1 "drift: default/iris-mlp" "$MLOP" --state "$STATE" drift --all
expect_exit 1 '"status": "drifted"' "$MLOP" --state "$STATE" drift iris-mlp --json
expect_exit 1 '"status": "missing"' "$MLOP" --state "$STATE" drift iris-mlp --json

step "re-applying the manifest puts the world back"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp
expect "registry.example.com/iris-mlp:v1" "$MLOP" --state "$STATE" status iris-mlp

step "a manual scale-down is drift too, and re-apply fixes it"
python3 - "$STATE" <<'PYEOF'
import json, sys
state = json.load(open(sys.argv[1]))
state["cluster"]["Deployments"]["default/iris-mlp"]["Replicas"] = 1
json.dump(state, open(sys.argv[1], "w"), indent=2)
PYEOF
expect_exit 1 "replicas 1 -> 2" "$MLOP" --state "$STATE" drift iris-mlp
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

echo
echo "tour passed: drift caught an image swap, a deleted Service, and a manual scale-down; re-apply fixed all three."
