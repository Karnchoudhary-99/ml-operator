package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairFixesTampering(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	tamperState(t, state)

	code, out, _ := runCLIState(t, state, "repair", "iris-mlp")
	if code != 0 {
		t.Fatalf("repair exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "repaired: default/iris-mlp") {
		t.Fatalf("out should name the repaired deployment, got %q", out)
	}
	if !strings.Contains(out, "repaired Deployment default/iris-mlp") {
		t.Fatalf("out should list the fixed Deployment, got %q", out)
	}
	if !strings.Contains(out, "repaired Service default/iris-mlp") {
		t.Fatalf("out should list the recreated Service, got %q", out)
	}

	// Drift agrees afterwards: everything is back in sync, and
	// the repair survived in the persisted state.
	if code, out, _ := runCLIState(t, state, "drift", "iris-mlp"); code != 0 {
		t.Fatalf("drift after repair exit %d, out %q", code, out)
	}
	if code, out, _ := runCLIState(t, state, "logs", "iris-mlp"); code != 0 || !strings.Contains(out, "DriftRepaired") {
		t.Fatalf("logs exit %d, out %q — want the DriftRepaired event", code, out)
	}
}

func TestRepairInSync(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	code, out, _ := runCLIState(t, state, "repair", "iris-mlp")
	if code != 0 || !strings.Contains(out, "in sync: default/iris-mlp") {
		t.Fatalf("repair exit %d, out %q", code, out)
	}
}

func TestRepairDryRun(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	tamperState(t, state)

	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	// A dry run exits 1 (it would have repaired something) and
	// writes nothing.
	code, out, _ := runCLIState(t, state, "repair", "iris-mlp", "--dry-run")
	if code != 1 || !strings.Contains(out, "would repair: default/iris-mlp") {
		t.Fatalf("repair --dry-run exit %d, out %q", code, out)
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("dry-run repair changed the state file")
	}
	if code, _, _ := runCLIState(t, state, "drift", "iris-mlp"); code != 1 {
		t.Fatalf("drift after dry run exit %d, want 1 (still drifted)", code)
	}
}

func TestRepairJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	tamperState(t, state)

	code, out, _ := runCLIState(t, state, "repair", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("repair --json exit %d, out %q", code, out)
	}
	var rep struct {
		Deployment string `json:"deployment"`
		Results    []struct {
			Kind    string `json:"kind"`
			Outcome string `json:"outcome"`
			Was     string `json:"was"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse repair JSON: %v (out %q)", err, out)
	}
	if rep.Deployment != "default/iris-mlp" {
		t.Fatalf("deployment=%q", rep.Deployment)
	}
	fixed := 0
	for _, res := range rep.Results {
		if res.Outcome == "repaired" {
			fixed++
			if res.Was != "drifted" && res.Was != "missing" {
				t.Fatalf("%s %s was=%s, want drifted/missing", res.Kind, res.Outcome, res.Was)
			}
		}
	}
	if fixed != 2 {
		t.Fatalf("fixed=%d, want 2 (Deployment + Service)", fixed)
	}
}

func TestRepairPaused(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	if code, _, _ := runCLIState(t, state, "pause", "iris-mlp"); code != 0 {
		t.Fatalf("pause exit %d", code)
	}
	tamperState(t, state)

	code, out, _ := runCLIState(t, state, "repair", "iris-mlp")
	if code != 1 || !strings.Contains(out, "left Deployment default/iris-mlp") {
		t.Fatalf("repair on paused exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "paused; resume") {
		t.Fatalf("out should name the pause, got %q", out)
	}
	// The tamper is still there: nothing was written.
	if code, _, _ := runCLIState(t, state, "drift", "iris-mlp"); code != 1 {
		t.Fatalf("drift after refused repair exit %d, want 1", code)
	}
}

func TestRepairUsageAndUnknown(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	if code, _, _ := runCLIState(t, state, "repair"); code != 2 {
		t.Fatalf("repair with no name exit %d, want 2", code)
	}
	if code, _, _ := runCLIState(t, state, "repair", "iris-mlp", "--all"); code != 2 {
		t.Fatalf("repair name + --all exit %d, want 2", code)
	}
	if code, _, _ := runCLIState(t, state, "repair", "nope"); code != 1 {
		t.Fatalf("repair unknown exit %d, want 1", code)
	}
}
