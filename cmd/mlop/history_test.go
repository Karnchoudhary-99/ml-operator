package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// history exercises the audit trail end to end through the CLI:
// apply records the first transition, ready records the phase
// flip, and the trail survives across invocations in the state file.
func TestHistoryFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	// No deployment yet: history errors instead of inventing one.
	if code, _, _ := runCLIState(t, state, "history", "iris-mlp"); code == 0 {
		t.Fatal("history for an unknown deployment exited 0")
	}

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}

	code, out, _ = runCLIState(t, state, "history", "iris-mlp")
	if code != 0 {
		t.Fatalf("history exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "gen 1") || !strings.Contains(out, "Progressing") {
		t.Fatalf("history after apply %q, want gen 1 Progressing", out)
	}
	if !strings.Contains(out, "registry.example.com/iris-mlp:v1") {
		t.Fatalf("history after apply %q, want the v1 image", out)
	}

	// Pods ready: the phase flips to Available, a second entry.
	code, out, _ = runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2")
	if code != 0 {
		t.Fatalf("ready exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "history", "iris-mlp")
	if code != 0 {
		t.Fatalf("history exit %d, out %q", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("history has %d lines, want 2 (Progressing then Available): %q", len(lines), out)
	}
	if !strings.Contains(lines[1], "Available") {
		t.Fatalf("second history line %q, want Available", lines[1])
	}

	// --tail 1 keeps only the latest entry.
	code, out, _ = runCLIState(t, state, "history", "iris-mlp", "--tail", "1")
	if code != 0 {
		t.Fatalf("history --tail exit %d, out %q", code, out)
	}
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "Available") {
		t.Fatalf("history --tail 1 = %q, want only the Available entry", out)
	}

	// --json returns the same trail as an array, oldest first.
	code, out, _ = runCLIState(t, state, "history", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("history --json exit %d, out %q", code, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("history --json is not valid JSON: %v (%q)", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("history --json has %d rows, want 2", len(rows))
	}
	if rows[0]["phase"] != "Progressing" || rows[1]["phase"] != "Available" {
		t.Fatalf("history --json phases = %v/%v, want Progressing/Available", rows[0]["phase"], rows[1]["phase"])
	}
	if rows[1]["generation"] != float64(1) || rows[1]["version"] != "v1" {
		t.Fatalf("history --json second row = %v, want generation 1 version v1", rows[1])
	}
}
