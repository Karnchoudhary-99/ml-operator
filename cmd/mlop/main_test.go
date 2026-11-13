package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI runs the CLI in-process with buffered output and a temp
// state file, returning the exit code and both streams.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	state := filepath.Join(t.TempDir(), "state.json")
	full := append([]string{"--state", state}, args...)
	code := run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// runCLIState runs the CLI against a fixed state file (shared
// across calls in one test) so multi-command flows can be tested.
func runCLIState(t *testing.T, state string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	full := append([]string{"--state", state}, args...)
	code := run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const irisV1 = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v1
    framework: onnx
    version: v1
  replicas: 2
`

func TestApplyAndStatusFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "applied default/iris-mlp (generation 1)") {
		t.Fatalf("apply output %q", out)
	}
	if !strings.Contains(out, "CreateDeployment") {
		t.Fatalf("apply should create the Deployment, got %q", out)
	}

	// A second apply of the same manifest is a no-op.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "unchanged default/iris-mlp (generation 1)") {
		t.Fatalf("re-apply exit %d, out %q", code, out)
	}

	// List status shows the deployment, still Progressing (no pods ready).
	code, out, _ = runCLIState(t, state, "status")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	if !strings.Contains(out, "iris-mlp") || !strings.Contains(out, "Progressing") || !strings.Contains(out, "0/2") {
		t.Fatalf("status list %q", out)
	}

	// Detail status shows the model and the requeue note.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status detail exit %d", code)
	}
	for _, want := range []string{"generation: 1", "ready:      0/2 replicas", "registry.example.com/iris-mlp:v1", "requeue:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status detail %q, want %q", out, want)
		}
	}

	// A new image bumps the generation and rolls.
	manifestV2 := writeManifest(t, strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:v2", 1))
	manifestV2 = strings.Replace(manifestV2, "version: v1", "version: v2", 1)
	code, out, _ = runCLIState(t, state, "apply", "-f", manifestV2)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 2)") {
		t.Fatalf("v2 apply exit %d, out %q", code, out)
	}
}

func TestApplyDryRunAndInvalid(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	code, out, _ := runCLIState(t, state, "apply", "-f", manifest, "--dry-run")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") {
		t.Fatalf("dry-run exit %d, out %q", code, out)
	}
	// A dry-run must not create state.
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a state file")
	}
	// A real apply after the dry-run still starts at generation 1.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "(generation 1)") {
		t.Fatalf("apply after dry-run exit %d, out %q", code, out)
	}

	bad := writeManifest(t, "name: bad\nspec:\n  model:\n    image: \"\"\n")
	code, _, errOut := runCLI(t, "apply", "-f", bad)
	if code == 0 {
		t.Fatalf("invalid manifest should fail")
	}
	if !strings.Contains(errOut, "must not be empty") {
		t.Fatalf("invalid manifest error %q", errOut)
	}

	code, _, errOut = runCLI(t, "apply", "-f", filepath.Join(t.TempDir(), "missing.yaml"))
	if code == 0 {
		t.Fatalf("missing manifest file should fail")
	}
	if !strings.Contains(errOut, "read") {
		t.Fatalf("missing file error %q", errOut)
	}

	code, _, _ = runCLI(t, "apply")
	if code == 0 {
		t.Fatalf("apply without -f should fail")
	}
}

func TestStatusErrors(t *testing.T) {
	code, out, _ := runCLI(t, "status")
	if code != 0 || !strings.Contains(out, "no deployments") {
		t.Fatalf("empty status exit %d, out %q", code, out)
	}
	code, _, errOut := runCLI(t, "status", "nope")
	if code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("unknown name exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLI(t, "status", "a", "b")
	if code == 0 {
		t.Fatalf("status with two names should fail")
	}
}

func TestVersionAndUnknownCommand(t *testing.T) {
	code, out, _ := runCLI(t, "version")
	if code != 0 || !strings.Contains(out, "mlop ") {
		t.Fatalf("version exit %d, out %q", code, out)
	}
	code, _, errOut := runCLI(t, "frobnicate")
	if code == 0 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown command exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLI(t)
	if code == 0 {
		t.Fatalf("no command should fail")
	}
}
