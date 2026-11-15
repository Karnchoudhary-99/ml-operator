package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifestDir writes one manifest file per map entry into a
// fresh temp directory and returns the directory path.
func writeManifestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const housePriceV1 = `
name: house-price
spec:
  model:
    image: registry.example.com/house-price:v1
    framework: onnx
    version: v1
  replicas: 1
`

// TestApplyDirHappyPath applies a two-manifest directory, then
// applies it again: the first run applies both, the second reports
// both unchanged (an identical re-apply is not an "apply").
func TestApplyDirHappyPath(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	dir := writeManifestDir(t, map[string]string{
		"a-iris-mlp.yaml":    irisV1,
		"b-house-price.yaml": housePriceV1,
		"README.txt":         "not a manifest, ignored\n",
	})
	// A subdirectory is skipped, not walked.
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "manifest.yaml"), []byte(irisV1), 0o644); err != nil {
		t.Fatalf("write nested manifest: %v", err)
	}

	code, out, _ := runCLIState(t, state, "apply", "-d", dir)
	if code != 0 {
		t.Fatalf("apply -d exit %d, out %q", code, out)
	}
	for _, want := range []string{
		"applied default/iris-mlp (generation 1)",
		"applied default/house-price (generation 1)",
		"summary: 2 applied, 0 unchanged, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply -d out %q, want %q", out, want)
		}
	}
	// Filename order: iris (a-...) is applied before house-price (b-...).
	if strings.Index(out, "iris-mlp") > strings.Index(out, "house-price") {
		t.Fatalf("apply -d did not apply in filename order: %q", out)
	}

	code, out, _ = runCLIState(t, state, "status")
	if code != 0 || !strings.Contains(out, "iris-mlp") || !strings.Contains(out, "house-price") {
		t.Fatalf("status after apply -d exit %d, out %q", code, out)
	}

	// A second run changes nothing: both are unchanged.
	code, out, _ = runCLIState(t, state, "apply", "-d", dir)
	if code != 0 || !strings.Contains(out, "unchanged default/iris-mlp (generation 1)") ||
		!strings.Contains(out, "summary: 0 applied, 2 unchanged, 0 failed") {
		t.Fatalf("re-apply -d exit %d, out %q", code, out)
	}
}

// TestApplyDirDryRun validates a directory without touching state:
// the valid files are listed, no state file is created, and a
// real apply afterwards still starts at generation 1.
func TestApplyDirDryRun(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	dir := writeManifestDir(t, map[string]string{
		"a-iris-mlp.yaml":    irisV1,
		"b-house-price.yaml": housePriceV1,
	})
	code, out, _ := runCLIState(t, state, "apply", "-d", dir, "--dry-run")
	if code != 0 {
		t.Fatalf("apply -d --dry-run exit %d, out %q", code, out)
	}
	for _, want := range []string{
		"valid: default/iris-mlp",
		"valid: default/house-price",
		"summary: 2 valid, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run out %q, want %q", out, want)
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a state file")
	}
	code, out, _ = runCLIState(t, state, "apply", "-d", dir)
	if code != 0 || !strings.Contains(out, "(generation 1)") {
		t.Fatalf("apply after dry-run exit %d, out %q", code, out)
	}
}
