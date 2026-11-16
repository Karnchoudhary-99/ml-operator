package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, _ := runCLI(t, "completion", shell)
		if code != 0 {
			t.Fatalf("completion %s exit %d", shell, code)
		}
		if !strings.Contains(out, "__complete-names") {
			t.Fatalf("completion %s does not complete deployment names", shell)
		}
		if !strings.Contains(out, "mlop") {
			t.Fatalf("completion %s output %q never mentions mlop", shell, out)
		}
	}
	// The bash script is the one the tour exercises live; it must
	// register the _mlop function.
	if code, out, _ := runCLI(t, "completion", "bash"); code != 0 || !strings.Contains(out, "complete -F _mlop mlop") {
		t.Fatalf("completion bash exit %d, registers _mlop: %v", code, strings.Contains(out, "complete -F _mlop mlop"))
	}
}

func TestCompletionCommandErrors(t *testing.T) {
	code, _, errOut := runCLI(t, "completion")
	if code == 0 || !strings.Contains(errOut, "Usage: mlop completion") {
		t.Fatalf("completion without a shell exit %d, err %q", code, errOut)
	}
	code, _, errOut = runCLI(t, "completion", "powershell")
	if code == 0 || !strings.Contains(errOut, "unknown shell") {
		t.Fatalf("completion powershell exit %d, err %q", code, errOut)
	}
	code, _, errOut = runCLI(t, "completion", "bash", "zsh")
	if code == 0 || !strings.Contains(errOut, "Usage: mlop completion") {
		t.Fatalf("completion with two shells exit %d, err %q", code, errOut)
	}
}

func TestCompleteNames(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	// Empty state: no output, exit 0 — a shell must never error
	// completing names just because nothing is deployed yet.
	code, out, _ := runCLIState(t, state, "__complete-names")
	if code != 0 || out != "" {
		t.Fatalf("__complete-names on empty state exit %d, out %q", code, out)
	}

	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	code, out, _ = runCLIState(t, state, "__complete-names")
	if code != 0 || out != "iris-mlp\n" {
		t.Fatalf("__complete-names exit %d, out %q", code, out)
	}

	// A namespaced deployment prints as ns/name, sorted with the
	// default-namespace one.
	other := writeManifest(t, "namespace: team-a\n"+irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", other); code != 0 {
		t.Fatalf("apply namespaced failed")
	}
	code, out, _ = runCLIState(t, state, "__complete-names")
	if code != 0 || out != "iris-mlp\nteam-a/iris-mlp\n" {
		t.Fatalf("__complete-names with two deployments exit %d, out %q", code, out)
	}

	code, _, errOut := runCLIState(t, state, "__complete-names", "extra")
	if code == 0 || !strings.Contains(errOut, "Usage: mlop __complete-names") {
		t.Fatalf("__complete-names with an argument exit %d, err %q", code, errOut)
	}
}
