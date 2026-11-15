package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestWatchAvailableAndTimeout exercises `mlop watch`: an already
// ready deployment exits 0 on the first tick, and a deployment
// that never becomes ready exits 2 after the timeout. Short
// intervals keep the test fast; the real defaults are 1s / 5m.
func TestWatchAvailableAndTimeout(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}

	// Not ready yet: watch times out (exit 2) and prints exactly
	// one line — the first Progressing snapshot, no spam.
	code, out, errOut := runCLIState(t, state, "watch", "iris-mlp", "--interval", "10ms", "--timeout", "120ms")
	if code != 2 || !strings.Contains(errOut, "timed out") {
		t.Fatalf("watch timeout exit %d, out %q, err %q", code, out, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "Progressing") || !strings.Contains(lines[0], "0/2 ready") {
		t.Fatalf("watch timeout printed %d lines, want exactly one Progressing line: %q", len(lines), out)
	}

	// Pods ready: the next watch exits 0 on the first tick.
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready failed")
	}
	code, out, _ = runCLIState(t, state, "watch", "iris-mlp", "--interval", "10ms", "--timeout", "5s")
	if code != 0 || !strings.Contains(out, "Available") || !strings.Contains(out, "2/2 ready") {
		t.Fatalf("watch available exit %d, out %q", code, out)
	}
}

// TestWatchDegraded drives a canary to abort (three failed model
// loads) so the deployment is Degraded, then checks watch exits 1
// on the first tick instead of waiting for the timeout.
func TestWatchDegraded(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	v1 := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", v1); code != 0 {
		t.Fatalf("apply v1 failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready v1 failed")
	}
	canary := writeManifest(t, irisCanary)
	if code, _, _ := runCLIState(t, state, "apply", "-f", canary); code != 0 {
		t.Fatalf("canary apply failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary"); code != 0 {
		t.Fatalf("ready canary failed")
	}
	// Three failed loads abort the rollout: the deployment is
	// Degraded while stable still serves v1.
	if code, _, _ := runCLIState(t, state, "load", "iris-mlp", "--canary", "--fail"); code != 0 {
		t.Fatalf("load --fail failed")
	}
	if code, _, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 {
		t.Fatalf("rollout status (strike 2) failed")
	}
	if code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 || !strings.Contains(out, "state:      aborted") {
		t.Fatalf("rollout status (strike 3) exit %d, out %q", code, out)
	}

	code, out, _ := runCLIState(t, state, "watch", "iris-mlp", "--interval", "10ms", "--timeout", "5s")
	if code != 1 || !strings.Contains(out, "Degraded") {
		t.Fatalf("watch degraded exit %d, out %q", code, out)
	}
}

func TestWatchErrors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	// No deployments at all: --all errors instead of watching nothing.
	if code, _, errOut := runCLIState(t, state, "watch", "--all"); code == 0 || !strings.Contains(errOut, "no deployments") {
		t.Fatalf("watch --all empty exit %d, err %q", code, errOut)
	}
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	if code, _, errOut := runCLIState(t, state, "watch", "nope"); code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("watch unknown exit %d, err %q", code, errOut)
	}
	if code, _, _ := runCLIState(t, state, "watch"); code == 0 {
		t.Fatalf("watch without a name should fail")
	}
	if code, _, _ := runCLIState(t, state, "watch", "iris-mlp", "extra"); code == 0 {
		t.Fatalf("watch with two names should fail")
	}
	if code, _, _ := runCLIState(t, state, "watch", "--all", "iris-mlp"); code == 0 {
		t.Fatalf("watch --all with a name should fail")
	}
	if code, _, errOut := runCLIState(t, state, "watch", "iris-mlp", "--interval", "0s"); code == 0 || !strings.Contains(errOut, "--interval must be positive") {
		t.Fatalf("watch --interval 0 exit %d, err %q", code, errOut)
	}
	if code, _, errOut := runCLIState(t, state, "watch", "iris-mlp", "--timeout", "0s"); code == 0 || !strings.Contains(errOut, "--timeout must be positive") {
		t.Fatalf("watch --timeout 0 exit %d, err %q", code, errOut)
	}
}

// TestWatchJSON checks the --json stream: one valid JSON object per
// transition, with the phase and the ready count in it.
func TestWatchJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready failed")
	}
	code, out, _ := runCLIState(t, state, "watch", "iris-mlp", "--json", "--interval", "10ms", "--timeout", "5s")
	if code != 0 {
		t.Fatalf("watch --json exit %d, out %q", code, out)
	}
	var ev watchEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ev); err != nil {
		t.Fatalf("watch --json is not valid JSON: %v (%q)", err, out)
	}
	if ev.Name != "iris-mlp" || ev.Namespace != "default" || ev.Phase != "Available" || ev.Ready != 2 || ev.Desired != 2 {
		t.Fatalf("watch --json event = %+v, want iris-mlp/default Available 2/2", ev)
	}
	if ev.Elapsed == "" {
		t.Fatalf("watch --json event has no elapsed time: %+v", ev)
	}
}
