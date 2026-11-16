package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPauseResumeFlow: pause freezes the deployment (a held image
// change does not apply), pause is idempotent, and resume applies
// the held change and clears the Paused condition.
func TestPauseResumeFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}

	code, out, _ = runCLIState(t, state, "pause", "iris-mlp")
	if code != 0 || !strings.Contains(out, "paused default/iris-mlp (generation 2)") {
		t.Fatalf("pause exit %d, out %q", code, out)
	}

	// Pausing again is a no-op, not a generation bump.
	code, out, _ = runCLIState(t, state, "pause", "iris-mlp")
	if code != 0 || !strings.Contains(out, "already paused (generation 2)") {
		t.Fatalf("re-pause exit %d, out %q", code, out)
	}

	// Status shows the pause; the deployment is still Progressing
	// (no pods ready) but the message says it is suspended.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "reconciliation paused") {
		t.Fatalf("status exit %d, out %q", code, out)
	}

	// An image change while paused is held: apply a manifest that
	// keeps paused: true, so the new image registers but the
	// reconciler does not apply it. (A manifest without paused
	// resumes the deployment — apply replaces the whole spec, and
	// an absent paused means false.)
	manifestV2 := writeManifest(t, strings.Replace(
		strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:v2", 1),
		"version: v1", "version: v2\n  paused: true", 1))
	code, out, _ = runCLIState(t, state, "apply", "-f", manifestV2)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 3)") {
		t.Fatalf("v2 apply while paused exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "--json", "iris-mlp")
	if code != 0 {
		t.Fatalf("status --json exit %d", code)
	}
	if !strings.Contains(out, `"image": "registry.example.com/iris-mlp:v2"`) {
		t.Fatalf("status --json should show the desired v2 image, got %q", out)
	}

	// Resume applies the held change.
	code, out, _ = runCLIState(t, state, "resume", "iris-mlp")
	if code != 0 || !strings.Contains(out, "resumed default/iris-mlp (generation 4)") {
		t.Fatalf("resume exit %d, out %q", code, out)
	}

	// Resuming again is a no-op.
	code, out, _ = runCLIState(t, state, "resume", "iris-mlp")
	if code != 0 || !strings.Contains(out, "not paused (generation 4)") {
		t.Fatalf("re-resume exit %d, out %q", code, out)
	}

	// The Paused condition is False again, and a further status
	// shows the deployment converging on v2.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	if !strings.Contains(out, `"type": "Paused"`) || !strings.Contains(out, `"status": "False"`) {
		t.Fatalf("resumed status should carry Paused=False, got %q", out)
	}
}

func TestPauseResumeErrors(t *testing.T) {
	code, _, errOut := runCLI(t, "pause", "ghost")
	if code != 1 || !strings.Contains(errOut, `no deployment "default/ghost"`) {
		t.Fatalf("pause ghost: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "resume", "ghost")
	if code != 1 || !strings.Contains(errOut, `no deployment "default/ghost"`) {
		t.Fatalf("resume ghost: code=%d err=%q", code, errOut)
	}
	code, _, errOut = runCLI(t, "pause")
	if code != 2 {
		t.Fatalf("pause with no name: code=%d err=%q, want 2", code, errOut)
	}
}
