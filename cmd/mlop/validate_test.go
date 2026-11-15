package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateManifest(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	code, out, _ := runCLIState(t, state, "validate", "-f", manifest)
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") || !strings.Contains(out, "no warnings") {
		t.Fatalf("validate clean exit %d, out %q", code, out)
	}
	// Validate never creates a state file.
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("validate created a state file")
	}
}

func TestValidateWarningsAndStrict(t *testing.T) {
	// :latest is valid but risky: a warning, not an error.
	latest := strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:latest", 1)
	manifest := writeManifest(t, latest)
	code, out, _ := runCLI(t, "validate", "-f", manifest)
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") || !strings.Contains(out, ":latest tag") {
		t.Fatalf("validate :latest exit %d, out %q", code, out)
	}
	// --strict turns the warning into a failure.
	code, out, _ = runCLI(t, "validate", "-f", manifest, "--strict")
	if code == 0 {
		t.Fatalf("validate --strict succeeded with a warning, out %q", out)
	}
	// --strict with a clean manifest still passes.
	clean := writeManifest(t, irisV1)
	code, out, _ = runCLI(t, "validate", "-f", clean, "--strict")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") {
		t.Fatalf("validate --strict clean exit %d, out %q", code, out)
	}
}

func TestValidateInvalid(t *testing.T) {
	bad := writeManifest(t, "name: bad\nspec:\n  model:\n    image: \"\"\n")
	code, _, errOut := runCLI(t, "validate", "-f", bad)
	if code == 0 || !strings.Contains(errOut, "is invalid") || !strings.Contains(errOut, "must not be empty") {
		t.Fatalf("validate invalid exit %d, err %q", code, errOut)
	}
	// An invalid manifest fails under --strict too, with the same
	// errors (strict only adds warnings to the failure set).
	code, _, errOut = runCLI(t, "validate", "-f", bad, "--strict")
	if code == 0 || !strings.Contains(errOut, "must not be empty") {
		t.Fatalf("validate --strict invalid exit %d, err %q", code, errOut)
	}
}

func TestValidateJSON(t *testing.T) {
	manifest := writeManifest(t, irisV1)
	code, out, _ := runCLI(t, "validate", "-f", manifest, "--json")
	if code != 0 {
		t.Fatalf("validate --json exit %d, out %q", code, out)
	}
	var view validateView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("validate --json is not valid JSON: %v (%q)", err, out)
	}
	if view.Deployment != "default/iris-mlp" || !view.Valid || view.Strict ||
		len(view.Errors) != 0 || len(view.Warnings) != 0 {
		t.Fatalf("validate view = %+v, want valid, non-strict, empty errors/warnings", view)
	}
	// The empty arrays are real arrays in the JSON, not null.
	if !strings.Contains(out, `"errors": []`) || !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("validate --json should print empty arrays, got %q", out)
	}

	// An invalid manifest reports Valid false with the errors.
	bad := writeManifest(t, "name: bad\nspec:\n  model:\n    image: \"\"\n")
	code, out, _ = runCLI(t, "validate", "-f", bad, "--json")
	if code == 0 {
		t.Fatalf("validate --json invalid exit %d", code)
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("validate --json invalid is not valid JSON: %v (%q)", err, out)
	}
	if view.Valid || len(view.Errors) == 0 || !strings.Contains(view.Errors[0], "must not be empty") {
		t.Fatalf("validate --json invalid view = %+v", view)
	}
	// A warning under --strict reports Valid false with the warning.
	latest := writeManifest(t, strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:latest", 1))
	code, out, _ = runCLI(t, "validate", "-f", latest, "--json", "--strict")
	if code == 0 {
		t.Fatalf("validate --json --strict exit %d", code)
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("validate --json --strict is not valid JSON: %v (%q)", err, out)
	}
	if view.Valid || !view.Strict || len(view.Warnings) == 0 {
		t.Fatalf("validate --json --strict view = %+v", view)
	}
}

func TestValidateStoredDeployment(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	// The stored deployment validates, and the check is read-only.
	code, out, _ := runCLIState(t, state, "validate", "iris-mlp")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") {
		t.Fatalf("validate stored exit %d, out %q", code, out)
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("validate changed the state file")
	}
	// Namespaced names work too.
	code, out, _ = runCLIState(t, state, "validate", "default/iris-mlp")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") {
		t.Fatalf("validate ns/name exit %d, out %q", code, out)
	}
	code, _, errOut := runCLIState(t, state, "validate", "no-such-model")
	if code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("validate unknown exit %d, err %q", code, errOut)
	}
}

func TestValidateErrors(t *testing.T) {
	manifest := writeManifest(t, irisV1)
	// Name and -f together.
	if code, _, _ := runCLI(t, "validate", "iris-mlp", "-f", manifest); code == 0 {
		t.Fatalf("validate with both a name and -f should fail")
	}
	// Neither name nor -f.
	if code, _, _ := runCLI(t, "validate"); code == 0 {
		t.Fatalf("validate with neither a name nor -f should fail")
	}
	// Missing manifest file.
	if code, _, errOut := runCLI(t, "validate", "-f", filepath.Join(t.TempDir(), "missing.yaml")); code == 0 || !strings.Contains(errOut, "read") {
		t.Fatalf("validate missing file exit %d, err %q", code, errOut)
	}
	// Unparseable manifest.
	broken := writeManifest(t, "name: broken\nspec:\n  model: [not-a-map]\n")
	if code, _, _ := runCLI(t, "validate", "-f", broken); code == 0 {
		t.Fatalf("validate of an unparseable manifest should fail")
	}
}
