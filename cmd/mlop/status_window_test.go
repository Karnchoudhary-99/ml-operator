package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStatusMaintenanceWindow pins the window lines end to end:
// a manifest whose window opens only tomorrow defers the apply
// (the model is never created), status names the wait and the
// next opening, and a manifest whose window is open today applies
// normally and clears the condition.
func TestStatusMaintenanceWindow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("Mon")
	today := time.Now().Format("Mon")

	closed := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  maintenanceWindow:\n    days: "+tomorrow+"\n    start: 02:00\n    end: 04:00\n", 1)
	manifest := writeManifest(t, closed)
	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "waiting for maintenance window") {
		t.Fatalf("apply output %q, want the deferral named", out)
	}
	if strings.Contains(out, "CreateDeployment") {
		t.Fatalf("apply output %q, want no cluster writes outside the window", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	for _, want := range []string{
		"window:     " + tomorrow + " 02:00–04:00 UTC (closed — next opens ",
		"WindowBlocked: True (WindowBlocked)",
		"waiting for maintenance window",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status %q, want %q", out, want)
		}
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	if detail.MaintenanceWindow == nil || len(detail.MaintenanceWindow.Days) != 1 ||
		detail.MaintenanceWindow.Days[0] != tomorrow || detail.MaintenanceWindow.Start != "02:00" {
		t.Fatalf("status --json window = %+v", detail.MaintenanceWindow)
	}

	// A window open today applies the same deployment, and the
	// condition flips back to False.
	open := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  maintenanceWindow:\n    days: "+today+"\n    start: 00:00\n    end: 23:59\n", 1)
	manifest = writeManifest(t, open)
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 2)") {
		t.Fatalf("apply in-window exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "window:     "+today+" 00:00–23:59 UTC (open now)") {
		t.Fatalf("status %q, want the open window named", out)
	}
	if !strings.Contains(out, "WindowBlocked: False (WindowOpen)") {
		t.Fatalf("status %q, want WindowBlocked cleared", out)
	}
}
