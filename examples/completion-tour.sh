#!/usr/bin/env bash
# mlop completion tour: generate the bash/zsh/fish scripts, syntax-check
# them, then drive the real bash completion function against a live
# state file: command names, flags, rollout's subcommand, and deployment
# names all complete, and a name already typed is not completed again.
#
# Usage: examples/completion-tour.sh
set -euo pipefail

cd "$(dirname "$0")/.."
source examples/tour-lib.sh
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MLOP="$WORK/mlop"
STATE="$WORK/state.json"

go build -o "$MLOP" ./cmd/mlop
# The completion scripts call bare `mlop`, and __complete-names reads
# the default state file: point both at this tour's build and state.
export PATH="$WORK:$PATH"
export MLOP_STATE="$STATE"

step() { echo; echo "=== $* ==="; }

# complete_words <cword> <words...>: run the _mlop completion
# function with COMP_WORDS/COMP_CWORD set as bash would, and print
# COMPREPLY one per line (or "(no completions)" when empty).
complete_words() {
	local cword="$1"; shift
	COMP_WORDS=("$@")
	COMP_CWORD="$cword"
	COMPREPLY=()
	_mlop
	if [ "${#COMPREPLY[@]}" -eq 0 ]; then
		echo "(no completions)"
	else
		printf '%s\n' "${COMPREPLY[@]}"
	fi
}

step "generate all three scripts and syntax-check them"
"$MLOP" completion bash > "$WORK/mlop.bash"
"$MLOP" completion zsh > "$WORK/mlop.zsh"
"$MLOP" completion fish > "$WORK/mlop.fish"
bash -n "$WORK/mlop.bash"
if command -v zsh >/dev/null; then
	zsh -n "$WORK/mlop.zsh"
else
	echo "zsh not installed, skipping zsh -n"
fi
grep -q "complete -c mlop" "$WORK/mlop.fish"
grep -q "__complete-names" "$WORK/mlop.bash" "$WORK/mlop.zsh" "$WORK/mlop.fish"

step "source the bash completion and complete command names"
source "$WORK/mlop.bash"
complete -p mlop | grep -q "_mlop"
expect "status" complete_words 1 mlop st
expect "completion" complete_words 1 mlop comp
expect "traffic" complete_words 1 mlop tr

step "complete flags and the completion shells"
expect "--json" complete_words 2 mlop status --
expect "--requests" complete_words 2 mlop traffic --
expect "--p95-ms" complete_words 2 mlop traffic --p
expect "bash" complete_words 2 mlop completion ""

step "no deployments yet: name completion is empty, not an error"
expect "(no completions)" complete_words 2 mlop status ""

step "apply iris-mlp, then its name completes"
expect "applied default/iris-mlp (generation 1)" "$MLOP" --state "$STATE" apply -f examples/iris-mlp.yaml
expect "iris-mlp" complete_words 2 mlop status ""
expect "iris-mlp" complete_words 3 mlop rollout status ""
expect "status" complete_words 2 mlop rollout ""

step "a name already typed is not completed again"
expect "(no completions)" complete_words 3 mlop status iris-mlp ""
expect "(no completions)" complete_words 4 mlop rollout status iris-mlp ""

step "--state is passed through to __complete-names"
expect "iris-mlp" complete_words 4 mlop --state "$STATE" status ""

step "-f completes files, --tail completes nothing"
expect "iris-mlp.yaml" complete_words 3 mlop apply -f examples/iris-mlp
expect "(no completions)" complete_words 4 mlop logs iris-mlp --tail ""

echo
echo "tour passed: bash/zsh/fish scripts generate and check out, live bash completion offers commands, flags, and deployment names."
