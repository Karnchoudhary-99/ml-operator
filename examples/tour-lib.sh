# Shared helpers for the example tour scripts (examples/*-tour.sh).
#
# Every assertion in a tour runs mlop, captures its whole output, and
# only then checks it. Never pipe mlop straight into grep -q: grep
# exits on the match while mlop still writes the lines after it, and
# under `set -o pipefail` that SIGPIPE kills the tour — flaky, seen
# twice in the model-load tour and once in the hardening tour during
# verification. Capturing first also prints the full output when an
# assertion fails, so a red tour says what mlop actually printed.
#
# Usage (after `cd` to the repo root, as the tours do):
#   source examples/tour-lib.sh
#   expect "phase:      Available" "$MLOP" --state "$STATE" status iris-mlp
#   expect_lines 2 "$MLOP" --state "$STATE" history iris-mlp

# expect <pattern> <command...>
# Run the command, capture its output, and fail unless the output
# (a basic regex, the same language `grep -q` used before) matches.
# A non-zero command exit fails the tour with the exit code.
# The output reaches grep through a here-string, not a pipe:
# `printf ... | grep -q` has a race — grep -q exits on the first
# match while printf is still writing a long output, printf dies
# on SIGPIPE, and under `set -o pipefail` that failure is the
# pipeline's status even though the pattern matched (the
# blue-green tour failed on CI exactly this way, 2026-11-17, on
# an output that did contain the pattern). With a here-string the
# check sees grep's exit code only, so an early match cannot fail.
expect() {
	local pat="$1"; shift
	local out rc
	out="$("$@")" && rc=0 || rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "expected $pat, but $* exited $rc" >&2
		exit 1
	fi
	if grep -q -- "$pat" <<< "$out"; then
		return 0
	fi
	echo "expected $pat in output of $* , got:" >&2
	echo "$out" >&2
	exit 1
}

# expect_lines <count> <command...>
# Run the command, capture its output, and fail unless it prints
# exactly <count> lines. A non-zero command exit fails the tour
# with the exit code.
expect_lines() {
	local want="$1"; shift
	local out rc lines
	out="$("$@")" && rc=0 || rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "expected $want lines, but $* exited $rc" >&2
		exit 1
	fi
	if [ -z "$out" ]; then
		lines=0
	else
		lines=$(printf '%s\n' "$out" | wc -l)
	fi
	if [ "$lines" -ne "$want" ]; then
		echo "expected $want lines from $* , got $lines:" >&2
		echo "$out" >&2
		exit 1
	fi
}
