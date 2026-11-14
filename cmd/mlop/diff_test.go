package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffNameAndManifest(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}

	// A fresh apply is already in sync: the plan is a lone NoOp.
	code, out, _ = runCLIState(t, state, "diff", "iris-mlp")
	if code != 0 || !strings.Contains(out, "in sync: default/iris-mlp (generation 1)") {
		t.Fatalf("diff exit %d, out %q", code, out)
	}

	// Diffing the v2 manifest previews the image change — without
	// applying it and without touching the state file.
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	manifestV2 := writeManifest(t, strings.Replace(strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:v2", 1), "version: v1", "version: v2", 1))
	code, out, _ = runCLIState(t, state, "diff", "-f", manifestV2)
	if code != 0 || !strings.Contains(out, "diff default/iris-mlp (generation 2)") {
		t.Fatalf("diff -f exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "UpdateDeployment default/iris-mlp") || !strings.Contains(out, "v1") || !strings.Contains(out, "v2") {
		t.Fatalf("diff -f should preview the image change, got %q", out)
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("diff changed the state file")
	}
	// The deployment is still v1: the real apply afterwards still
	// bumps the generation to 2.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifestV2)
	if code != 0 || !strings.Contains(out, "(generation 2)") {
		t.Fatalf("apply after diff exit %d, out %q", code, out)
	}
}

func TestDiffFreshManifest(t *testing.T) {
	// Diffing a manifest for a deployment that does not exist yet
	// previews the creates, and creates no state file.
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	code, out, _ := runCLIState(t, state, "diff", "-f", manifest)
	if code != 0 || !strings.Contains(out, "diff default/iris-mlp (generation 1)") {
		t.Fatalf("diff -f exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "CreateDeployment default/iris-mlp") || !strings.Contains(out, "CreateService") {
		t.Fatalf("fresh diff should preview creates, got %q", out)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("diff created a state file")
	}
}

func TestDiffJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	code, out, _ := runCLIState(t, state, "diff", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("diff --json exit %d, out %q", code, out)
	}
	var view diffView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("diff --json is not valid JSON: %v (%q)", err, out)
	}
	if view.Deployment != "default/iris-mlp" || view.Generation != 1 || !view.InSync {
		t.Fatalf("diff view = %+v, want in-sync generation 1", view)
	}
	if len(view.Actions) != 1 || view.Actions[0].Kind != "NoOp" {
		t.Fatalf("diff actions = %+v, want a single NoOp", view.Actions)
	}

	manifestV2 := writeManifest(t, strings.Replace(strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:v2", 1), "version: v1", "version: v2", 1))
	code, out, _ = runCLIState(t, state, "diff", "-f", manifestV2, "--json")
	if code != 0 {
		t.Fatalf("diff -f --json exit %d, out %q", code, out)
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("diff -f --json is not valid JSON: %v (%q)", err, out)
	}
	if view.InSync || view.Generation != 2 || len(view.Actions) != 1 || view.Actions[0].Kind != "UpdateDeployment" {
		t.Fatalf("diff view = %+v, want one UpdateDeployment at generation 2", view)
	}
}

func TestDiffErrors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	// Unknown deployment.
	if code, _, _ := runCLIState(t, state, "diff", "no-such-model"); code == 0 {
		t.Fatalf("diff of an unknown deployment should fail")
	}
	// Name and -f together.
	if code, _, _ := runCLIState(t, state, "diff", "iris-mlp", "-f", manifest); code == 0 {
		t.Fatalf("diff with both a name and -f should fail")
	}
	// Neither name nor -f.
	if code, _, _ := runCLI(t, "diff"); code == 0 {
		t.Fatalf("diff with neither a name nor -f should fail")
	}
	// Invalid manifest.
	bad := writeManifest(t, "name: bad\nspec:\n  model:\n    image: \"\"\n")
	if code, _, _ := runCLIState(t, state, "diff", "-f", bad); code == 0 {
		t.Fatalf("diff of an invalid manifest should fail")
	}
}
