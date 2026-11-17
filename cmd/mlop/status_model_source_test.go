package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusModelSource pins the model-source lines end to
// end: a manifest with a modelSource applies, status names the
// source (URI, artifact path, verification), --json carries
// it, diff previews a source change, and a plain manifest has
// no modelSource line at all.
func TestStatusModelSource(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	sha := "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807"

	sourced := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  modelSource:\n    uri: https://artifacts.example.com/models/iris-mlp.onnx\n    sha256: "+sha+"\n", 1)
	manifest := writeManifest(t, sourced)
	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "applied default/iris-mlp (generation 1)") {
		t.Fatalf("apply output %q", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "modelSource:  https://artifacts.example.com/models/iris-mlp.onnx -> /models/iris-mlp.onnx (sha256: 9f2c4a1b8e77… verified)") {
		t.Fatalf("status %q, want the modelSource line", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	m := detail.ModelSource
	if m == nil || m.URI != "https://artifacts.example.com/models/iris-mlp.onnx" || m.SHA256 != sha {
		t.Fatalf("status --json modelSource = %+v", m)
	}

	// Diff previews a source change without applying it.
	moved := strings.Replace(sourced, "iris-mlp.onnx", "iris-mlp-v2.onnx", 1)
	movedManifest := writeManifest(t, moved)
	code, out, _ = runCLIState(t, state, "diff", "-f", movedManifest)
	if code != 0 {
		t.Fatalf("diff exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "model download init container changed") {
		t.Fatalf("diff %q, want the init container change named", out)
	}

	// A plain manifest has no modelSource line at all.
	plain := writeManifest(t, irisV1)
	code, out, _ = runCLIState(t, state, "apply", "-f", plain)
	if code != 0 {
		t.Fatalf("apply plain exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if strings.Contains(out, "modelSource:") {
		t.Fatalf("status %q, unsourced deployment should show no modelSource line", out)
	}
}
