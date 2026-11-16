#!/usr/bin/env bash
# mlop repair tour: apply v1 and bring it Available, then simulate
# out-of-band edits to the live objects (the way a kubectl edit or
# a manual scale would) and watch repair put the desired state back
# — except the leftover canary, which repair reports and never
# deletes. Every step asserts the expected output, so the script is
# a demo and a smoke test in one.
#
# Usage: examples/repair-tour.sh
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
# Like expect, but the command must exit with <code> (repair exits
# 1 when something is left unrepaired, which is the success case
# for the leftover-canary step).
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

# tamper_state: rewrite the persisted cluster JSON the way an
# out-of-band edit would — the registered spec is untouched, only
# the live objects move: the image is swapped, the Service is
# deleted, and a leftover canary Deployment appears that nothing
# desires.
tamper_state() {
	python3 - "$STATE" <<'PYEOF'
import copy, json, sys
state = json.load(open(sys.argv[1]))
cluster = state["cluster"]
dep = cluster["Deployments"]["default/iris-mlp"]
dep["Container"]["Image"] = "registry.example.com/iris-mlp:v99"
del cluster["Services"]["default/iris-mlp"]
canary = copy.deepcopy(dep)
canary["Name"] = "iris-mlp-canary"
canary["Canary"] = True
cluster["Deployments"]["default/iris-mlp-canary"] = canary
json.dump(state, open(sys.argv[1], "w"), indent=2)
PYEOF
}

# tamper_image: swap just the live image, the way a kubectl edit
# would.
tamper_image() {
	python3 - "$STATE" <<'PYEOF'
import json, sys
state = json.load(open(sys.argv[1]))
state["cluster"]["Deployments"]["default/iris-mlp"]["Container"]["Image"] = "registry.example.com/iris-mlp:v99"
json.dump(state, open(sys.argv[1], "w"), indent=2)
PYEOF
}

# untamper_canary: the human removes the leftover canary by hand —
# the decision repair refuses to make on its own.
untamper_canary() {
	python3 - "$STATE" <<'PYEOF'
import json, sys
state = json.load(open(sys.argv[1]))
del state["cluster"]["Deployments"]["default/iris-mlp-canary"]
json.dump(state, open(sys.argv[1], "w"), indent=2)
PYEOF
}

step "apply iris-mlp v1 and bring it Available"
"$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
"$MLOP" --state "$STATE" ready iris-mlp --replicas 2
expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp

step "a clean world repairs nothing"
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" repair iris-mlp

step "out-of-band edits: image swapped, Service deleted, leftover canary"
tamper_state
expect_exit 1 "Deployment default/iris-mlp: drifted" "$MLOP" --state "$STATE" drift iris-mlp
expect_exit 1 "Deployment default/iris-mlp-canary: unexpected" "$MLOP" --state "$STATE" drift iris-mlp

step "dry run reports what would change and writes nothing"
expect_exit 1 "would repair: default/iris-mlp" "$MLOP" --state "$STATE" repair iris-mlp --dry-run
expect_exit 1 "would repair Deployment default/iris-mlp" "$MLOP" --state "$STATE" repair iris-mlp --dry-run
expect_exit 1 "Deployment default/iris-mlp: drifted" "$MLOP" --state "$STATE" drift iris-mlp

step "repair fixes the drifted Deployment and the missing Service — but not the canary"
repair_out=""
repair_rc=0
repair_out="$("$MLOP" --state "$STATE" repair iris-mlp)" && repair_rc=0 || repair_rc=$?
if [ "$repair_rc" -ne 1 ]; then
	echo "expected exit 1 from repair (leftover canary), got $repair_rc" >&2
	exit 1
fi
for pat in "repaired: default/iris-mlp" "repaired Deployment default/iris-mlp" 'image "registry.example.com/iris-mlp:v99" -> "registry.example.com/iris-mlp:v1"' "repaired Service default/iris-mlp" "left Deployment default/iris-mlp-canary (unexpected)"; do
	if ! printf '%s\n' "$repair_out" | grep -q -- "$pat"; then
		echo "expected $pat in repair output, got:" >&2
		echo "$repair_out" >&2
		exit 1
	fi
done
# (No `mlop status` here: status reconciles, and the reconciler
# garbage-collects the leftover canary — the next step asserts
# drift still sees it.)
expect "DriftRepaired" "$MLOP" --state "$STATE" logs iris-mlp

step "drift now reports only the leftover canary"
expect_exit 1 "Deployment default/iris-mlp-canary: unexpected" "$MLOP" --state "$STATE" drift iris-mlp

step "the human deletes the leftover canary; the world is in sync again"
untamper_canary
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" repair iris-mlp

step "a paused deployment is refused outright"
"$MLOP" --state "$STATE" pause iris-mlp
tamper_image
expect_exit 1 "left Deployment default/iris-mlp (drifted)" "$MLOP" --state "$STATE" repair iris-mlp
expect_exit 1 "paused; resume" "$MLOP" --state "$STATE" repair iris-mlp
expect_exit 1 "Deployment default/iris-mlp: drifted" "$MLOP" --state "$STATE" drift iris-mlp
"$MLOP" --state "$STATE" resume iris-mlp
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp
tamper_image
expect "repaired: default/iris-mlp" "$MLOP" --state "$STATE" repair iris-mlp
expect "in sync: default/iris-mlp" "$MLOP" --state "$STATE" drift iris-mlp

echo
echo "tour passed: repair fixed an image swap and a deleted Service, left a leftover canary standing, and refused a paused deployment."
