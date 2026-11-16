package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tamperState edits the persisted cluster JSON the way an
// out-of-band kubectl edit would: it rewrites the live Deployment
// image and deletes the live Service, without touching the
// registered spec. The state file uses the Go structs' capitalized
// field names.
func tamperState(t *testing.T, state string) {
	t.Helper()
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	cluster := s["cluster"].(map[string]any)
	deps := cluster["Deployments"].(map[string]any)
	dep := deps["default/iris-mlp"].(map[string]any)
	dep["Container"].(map[string]any)["Image"] = "registry.example.com/iris-mlp:v99"
	svcs := cluster["Services"].(map[string]any)
	delete(svcs, "default/iris-mlp")
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("encode state: %v", err)
	}
	if err := os.WriteFile(state, out, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func TestDriftInSync(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	code, out, _ := runCLIState(t, state, "drift", "iris-mlp")
	if code != 0 || !strings.Contains(out, "in sync: default/iris-mlp") {
		t.Fatalf("drift exit %d, out %q", code, out)
	}

	// Drift is a read: the state file must be byte-identical
	// afterwards.
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if code, _, _ := runCLIState(t, state, "drift", "--all", "--json"); code != 0 {
		t.Fatalf("drift --all exit %d", code)
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("drift changed the state file")
	}
}

func TestDriftDetectsTampering(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	tamperState(t, state)

	code, out, _ := runCLIState(t, state, "drift", "iris-mlp")
	if code != 1 {
		t.Fatalf("drift exit %d, want 1, out %q", code, out)
	}
	for _, want := range []string{
		"drift: default/iris-mlp",
		"Deployment default/iris-mlp: drifted",
		"v99", "v1", // the image move, live -> desired
		"Service default/iris-mlp: missing",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("drift output %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "in sync") {
		t.Fatalf("drifted output should not say in sync: %q", out)
	}

	// JSON form carries the same verdicts, and the same exit code.
	code, out, _ = runCLIState(t, state, "drift", "iris-mlp", "--json")
	if code != 1 {
		t.Fatalf("drift --json exit %d, want 1", code)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse drift JSON: %v", err)
	}
	if rep["inSync"] != false || rep["deployment"] != "default/iris-mlp" {
		t.Fatalf("drift JSON verdict wrong: %v", rep)
	}
}

func TestDriftAll(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	second := strings.Replace(irisV1, "iris-mlp", "iris-mlp-2", 1)
	for _, m := range []string{irisV1, second} {
		manifest := writeManifest(t, m)
		if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
			t.Fatalf("apply exit %d", code)
		}
	}

	code, out, _ := runCLIState(t, state, "drift", "--all")
	if code != 0 {
		t.Fatalf("drift --all exit %d, want 0, out %q", code, out)
	}
	if !strings.Contains(out, "in sync: default/iris-mlp") || !strings.Contains(out, "in sync: default/iris-mlp-2") {
		t.Fatalf("drift --all should list both deployments in sync, got %q", out)
	}

	tamperState(t, state)
	code, out, _ = runCLIState(t, state, "drift", "--all")
	if code != 1 {
		t.Fatalf("drift --all exit %d, want 1, out %q", code, out)
	}
	if !strings.Contains(out, "drift: default/iris-mlp") || !strings.Contains(out, "in sync: default/iris-mlp-2") {
		t.Fatalf("drift --all should report one drifted, one in sync, got %q", out)
	}
}

func TestDriftPausedNote(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	if code, _, _ := runCLIState(t, state, "pause", "iris-mlp"); code != 0 {
		t.Fatalf("pause exit %d", code)
	}
	tamperState(t, state)

	code, out, _ := runCLIState(t, state, "drift", "iris-mlp")
	if code != 1 {
		t.Fatalf("drift exit %d, want 1, out %q", code, out)
	}
	if !strings.Contains(out, "paused; drift will not be corrected until resume") {
		t.Fatalf("drift on a paused deployment should carry the pause note, got %q", out)
	}
}

func TestDriftUsageErrors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	// Unknown deployment: error, exit 1.
	if code, _, errOut := runCLIState(t, state, "drift", "nope"); code != 1 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("drift unknown: exit %d, stderr %q", code, errOut)
	}
	// Name and --all together, and neither: usage error, exit 2.
	if code, _, _ := runCLIState(t, state, "drift", "iris-mlp", "--all"); code != 2 {
		t.Fatalf("drift name+--all exit %d, want 2", code)
	}
	if code, _, _ := runCLIState(t, state, "drift"); code != 2 {
		t.Fatalf("drift bare exit %d, want 2", code)
	}
}
