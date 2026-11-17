package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScaleFlow: scale up, no-op at the same count, scale down to
// 1 and then to 0, --dry-run writes nothing, and the Scaled event
// lands in the logs.
func TestScaleFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}

	code, out, _ = runCLIState(t, state, "scale", "iris-mlp", "--replicas", "4")
	if code != 0 || !strings.Contains(out, "scaled default/iris-mlp (generation 2): 2 -> 4 replicas") {
		t.Fatalf("scale up exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "0/4") {
		t.Fatalf("status after scale up exit %d, out %q, want 0/4", code, out)
	}

	// Scale to the current count is a no-op, not a generation bump.
	code, out, _ = runCLIState(t, state, "scale", "iris-mlp", "--replicas", "4")
	if code != 0 || !strings.Contains(out, "already at 4 replicas (generation 2)") {
		t.Fatalf("no-op scale exit %d, out %q", code, out)
	}

	// Scale down, then to zero (stops serving, still a valid spec).
	code, out, _ = runCLIState(t, state, "scale", "iris-mlp", "--replicas", "1")
	if code != 0 || !strings.Contains(out, "scaled default/iris-mlp (generation 3): 4 -> 1 replicas") {
		t.Fatalf("scale down exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "scale", "--replicas", "0", "iris-mlp")
	if code != 0 || !strings.Contains(out, "scaled default/iris-mlp (generation 4): 1 -> 0 replicas") {
		t.Fatalf("scale to zero exit %d, out %q (flags after the name must parse)", code, out)
	}

	// The manual scale is in the event log.
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "Scaled") || !strings.Contains(out, "scaled from 4 to 1 replicas") {
		t.Fatalf("logs exit %d, out %q, want the Scaled event", code, out)
	}

	// --dry-run prints the change and writes nothing: generation
	// and desired replicas are unchanged afterwards.
	code, out, _ = runCLIState(t, state, "scale", "iris-mlp", "--replicas", "3", "--dry-run")
	if code != 0 || !strings.Contains(out, "would scale default/iris-mlp from 0 to 3 replicas (generation 4 -> 5)") {
		t.Fatalf("dry-run exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 || !strings.Contains(out, `"generation": 4`) || !strings.Contains(out, `"desired": 0`) {
		t.Fatalf("status after dry-run exit %d, out %q, want generation 4 / desired 0", code, out)
	}
}

func TestScaleErrors(t *testing.T) {
	code, _, errOut := runCLI(t, "scale", "ghost", "--replicas", "2")
	if code != 1 || !strings.Contains(errOut, `no deployment "default/ghost"`) {
		t.Fatalf("scale ghost: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "scale", "--replicas", "2")
	if code != 2 {
		t.Fatalf("scale with no name: code=%d err=%q, want 2", code, errOut)
	}
	code, _, errOut = runCLI(t, "scale", "iris-mlp")
	if code != 2 || !strings.Contains(errOut, "--replicas N is required") {
		t.Fatalf("scale without --replicas: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "scale", "iris-mlp", "--replicas", "-1")
	if code != 2 || !strings.Contains(errOut, "--replicas N is required") {
		t.Fatalf("scale --replicas -1: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "scale", "iris-mlp", "--replicas", "abc")
	if code != 2 {
		t.Fatalf("scale --replicas abc: code=%d err=%q, want 2", code, errOut)
	}
}

// TestScaleRefusesAutoscaling: a deployment with autoscaling on
// refuses a manual scale — the reconciler would overwrite
// spec.replicas with the scaler's decision on the next reconcile,
// so accepting it would be a lie.
func TestScaleRefusesAutoscaling(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	autoscaled := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  autoscaling:\n    minReplicas: 1\n    maxReplicas: 5\n    targetCPUUtilization: 70\n", 1)
	manifest := writeManifest(t, autoscaled)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	code, _, errOut := runCLIState(t, state, "scale", "iris-mlp", "--replicas", "3")
	if code != 1 || !strings.Contains(errOut, "has autoscaling enabled (min 1, max 5)") || !strings.Contains(errOut, "would be reverted") {
		t.Fatalf("scale autoscaled: code=%d err=%q, want the autoscaling refusal", code, errOut)
	}
	// The refusal wrote nothing: still generation 1 at 2 replicas.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 || !strings.Contains(out, `"generation": 1`) || !strings.Contains(out, `"desired": 2`) {
		t.Fatalf("status after refused scale exit %d, out %q", code, out)
	}
}

// TestScaleRefusesPaused: like rollback, a paused deployment
// refuses a scale instead of silently swallowing the change.
func TestScaleRefusesPaused(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatal("apply failed")
	}
	if code, _, _ := runCLIState(t, state, "pause", "iris-mlp"); code != 0 {
		t.Fatal("pause failed")
	}
	code, _, errOut := runCLIState(t, state, "scale", "iris-mlp", "--replicas", "3")
	if code != 1 || !strings.Contains(errOut, "is paused; resume it first") {
		t.Fatalf("scale paused: code=%d err=%q, want a paused refusal", code, errOut)
	}
}
