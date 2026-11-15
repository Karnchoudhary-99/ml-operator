package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusJSON(t *testing.T) {
	// Empty state: an empty array, not an error.
	code, out, _ := runCLI(t, "status", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty status --json exit %d, out %q", code, out)
	}

	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	code, out, _ = runCLIState(t, state, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var rows []statusListView
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	if len(rows) != 1 || rows[0].Name != "iris-mlp" || rows[0].Namespace != "default" ||
		rows[0].Phase != "Progressing" || rows[0].Ready != 0 || rows[0].Desired != 2 ||
		rows[0].Version != "v1" {
		t.Fatalf("status list view = %+v", rows)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status detail --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status detail --json is not valid JSON: %v (%q)", err, out)
	}
	if detail.Name != "iris-mlp" || detail.Generation != 1 || detail.ObservedGeneration != 1 ||
		detail.Phase != "Progressing" || detail.Ready != 0 || detail.Desired != 2 ||
		detail.Image != "registry.example.com/iris-mlp:v1" || detail.Framework != "onnx" ||
		detail.Version != "v1" || detail.Strategy != "RollingUpdate" || !detail.Requeue {
		t.Fatalf("status detail view = %+v", detail)
	}
	if detail.Availability == nil || detail.Availability.EffectiveMinAvailable(2) != 1 ||
		!detail.Availability.AntiAffinityEnabled() || detail.Availability.EffectiveMaxSkew() != 1 {
		t.Fatalf("status detail availability = %+v, want the defaults (minAvailable 1/2, anti-affinity on, maxSkew 1)", detail.Availability)
	}
	if len(detail.Conditions) == 0 {
		t.Fatalf("status detail view has no conditions: %+v", detail)
	}
}

func TestLogsJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	// No events yet: an empty array.
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	code, out, _ := runCLIState(t, state, "logs", "iris-mlp", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty logs --json exit %d, out %q", code, out)
	}

	// Drive a real event: promote a canary, then roll back (the
	// rollback records a RolledBack event).
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready exit %d", code)
	}
	canary := writeManifest(t, irisV2Canary)
	if code, _, _ := runCLIState(t, state, "apply", "-f", canary); code != 0 {
		t.Fatalf("apply canary exit %d", code)
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary"); code != 0 {
		t.Fatalf("ready canary exit %d", code)
	}
	time.Sleep(2 * time.Second)
	if code, _, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 {
		t.Fatalf("rollout status exit %d", code)
	}
	time.Sleep(2 * time.Second)
	if code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 || !strings.Contains(out, "promoted") {
		t.Fatalf("rollout promote exit %d, out %q", code, out)
	}
	if code, _, _ := runCLIState(t, state, "rollback", "iris-mlp"); code != 0 {
		t.Fatalf("rollback exit %d", code)
	}

	code, out, _ = runCLIState(t, state, "logs", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("logs --json exit %d, out %q", code, out)
	}
	var rows []logEventView
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("logs --json is not valid JSON: %v (%q)", err, out)
	}
	found := false
	for _, e := range rows {
		if e.Reason == "RolledBack" {
			found = true
			if e.Deployment != "default/iris-mlp" || e.Count < 1 || e.LastSeen.IsZero() {
				t.Fatalf("RolledBack event view wrong: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("logs --json has no RolledBack event: %+v", rows)
	}

	// --tail still applies in JSON mode.
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp", "--json", "--tail", "1")
	if code != 0 {
		t.Fatalf("logs --json --tail exit %d, out %q", code, out)
	}
	rows = nil
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("logs --json --tail is not valid JSON: %v (%q)", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("logs --tail 1 returned %d rows, want 1", len(rows))
	}
}

func TestStatusConditionAges(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply exit %d", code)
	}

	// Text detail: every condition line carries an age ("just now"
	// — the apply just stamped them) and the real reason.
	code, out, _ := runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "Ready: False (Provisioning)") || !strings.Contains(out, "just now") {
		t.Fatalf("status detail missing condition age/reason:\n%s", out)
	}

	// JSON detail: the transition time is present and non-zero.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status detail --json is not valid JSON: %v (%q)", err, out)
	}
	if len(detail.Conditions) == 0 {
		t.Fatalf("status detail view has no conditions: %+v", detail)
	}
	for _, c := range detail.Conditions {
		if c.LastTransitionTime.IsZero() {
			t.Fatalf("condition %s has a zero transition time in JSON: %+v", c.Type, c)
		}
	}
}

func TestFormatConditionAge(t *testing.T) {
	now := time.Date(2026, 11, 14, 21, 30, 0, 0, time.UTC)
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "unknown"},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-2 * time.Hour), "2h ago"},
		{now.Add(-3 * 24 * time.Hour), "3d ago"},
		{now.Add(time.Minute), "just now"}, // clock skew: never a negative age
	}
	for _, tc := range cases {
		if got := formatConditionAge(tc.at, now); got != tc.want {
			t.Errorf("formatConditionAge(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}
