package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusMetadata pins the labels/annotations lines end to end:
// a manifest with metadata applies, status names the labels and
// annotations, --json carries them, diff previews a label change,
// and a plain manifest has neither line at all.
func TestStatusMetadata(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	labelled := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  labels:\n    team: search\n    cost-center: ml-platform\n  annotations:\n    owner: team-search\n    runbook: https://example.com/runbooks/iris-mlp\n", 1)
	manifest := writeManifest(t, labelled)
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
	if !strings.Contains(out, "labels:       cost-center=ml-platform, team=search") {
		t.Fatalf("status %q, want the labels line", out)
	}
	if !strings.Contains(out, "annotations:  owner=team-search, runbook=https://example.com/runbooks/iris-mlp") {
		t.Fatalf("status %q, want the annotations line", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	if detail.Labels["team"] != "search" || detail.Labels["cost-center"] != "ml-platform" {
		t.Fatalf("status --json labels = %v", detail.Labels)
	}
	if detail.Annotations["owner"] != "team-search" {
		t.Fatalf("status --json annotations = %v", detail.Annotations)
	}

	// Diff previews a label change without applying it.
	relabeled := strings.Replace(labelled, "team: search", "team: ranking", 1)
	relabeledManifest := writeManifest(t, relabeled)
	code, out, _ = runCLIState(t, state, "diff", "-f", relabeledManifest)
	if code != 0 {
		t.Fatalf("diff exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "labels changed") {
		t.Fatalf("diff %q, want the labels change named", out)
	}

	// A plain manifest has neither line at all.
	plain := writeManifest(t, irisV1)
	code, out, _ = runCLIState(t, state, "apply", "-f", plain)
	if code != 0 {
		t.Fatalf("apply plain exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if strings.Contains(out, "labels:") || strings.Contains(out, "annotations:") {
		t.Fatalf("status %q, a deployment without metadata should show no labels/annotations line", out)
	}
}
