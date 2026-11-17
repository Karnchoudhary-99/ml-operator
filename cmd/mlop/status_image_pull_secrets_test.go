package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusImagePullSecrets pins the pull-secret lines end
// to end: a manifest with imagePullSecrets applies, status
// names the secrets, --json carries them, diff previews a
// rotation, and a plain manifest has no imagePullSecrets line
// at all.
func TestStatusImagePullSecrets(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	secreted := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  imagePullSecrets: regcred, ghcr-creds\n", 1)
	manifest := writeManifest(t, secreted)
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
	if !strings.Contains(out, "imagePullSecrets: regcred, ghcr-creds") {
		t.Fatalf("status %q, want the imagePullSecrets line", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	if len(detail.ImagePullSecrets) != 2 || detail.ImagePullSecrets[0] != "regcred" || detail.ImagePullSecrets[1] != "ghcr-creds" {
		t.Fatalf("status --json imagePullSecrets = %v", detail.ImagePullSecrets)
	}

	// Diff previews a rotation without applying it.
	rotated := strings.Replace(secreted, "ghcr-creds", "ghcr-creds-v2", 1)
	rotatedManifest := writeManifest(t, rotated)
	code, out, _ = runCLIState(t, state, "diff", "-f", rotatedManifest)
	if code != 0 {
		t.Fatalf("diff exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "imagePullSecrets changed") {
		t.Fatalf("diff %q, want the imagePullSecrets change named", out)
	}

	// A plain manifest has no imagePullSecrets line at all.
	plain := writeManifest(t, irisV1)
	code, out, _ = runCLIState(t, state, "apply", "-f", plain)
	if code != 0 {
		t.Fatalf("apply plain exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if strings.Contains(out, "imagePullSecrets:") {
		t.Fatalf("status %q, a deployment without secrets should show no imagePullSecrets line", out)
	}
}
