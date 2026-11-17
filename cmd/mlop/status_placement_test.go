package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusPlacement pins the placement lines end to end: a
// manifest with nodeSelector + tolerations applies, status names
// the placement and --json carries it, diff previews a placement
// change, and a plain manifest has no placement line at all.
func TestStatusPlacement(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	placed := strings.Replace(irisV1, "  replicas: 2\n",
		"  replicas: 2\n  placement:\n    nodeSelector:\n      accelerator: nvidia-tesla-t4\n    tolerations: gpu=true:NoSchedule, dedicated:Exists:NoExecute:300\n", 1)
	manifest := writeManifest(t, placed)
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
	if !strings.Contains(out, "placement:    nodeSelector accelerator=nvidia-tesla-t4, tolerations gpu=true:NoSchedule, dedicated:Exists:NoExecute:300") {
		t.Fatalf("status %q, want the placement line", out)
	}

	code, out, _ = runCLIState(t, state, "status", "iris-mlp", "--json")
	if code != 0 {
		t.Fatalf("status --json exit %d, out %q", code, out)
	}
	var detail statusDetailView
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("status --json is not valid JSON: %v (%q)", err, out)
	}
	p := detail.Placement
	if p == nil || p.NodeSelector["accelerator"] != "nvidia-tesla-t4" || len(p.Tolerations) != 2 ||
		p.Tolerations[0].Value != "true" || p.Tolerations[1].Operator != "Exists" ||
		p.Tolerations[1].TolerationSeconds == nil || *p.Tolerations[1].TolerationSeconds != 300 {
		t.Fatalf("status --json placement = %+v", p)
	}

	// Diff previews a placement change without applying it.
	moved := strings.Replace(placed, "nvidia-tesla-t4", "nvidia-tesla-v100", 1)
	movedManifest := writeManifest(t, moved)
	code, out, _ = runCLIState(t, state, "diff", "-f", movedManifest)
	if code != 0 {
		t.Fatalf("diff exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "nodeSelector changed") {
		t.Fatalf("diff %q, want the nodeSelector change named", out)
	}

	// Re-applying the same manifest is a no-op: placement is
	// compared by value.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "unchanged default/iris-mlp (generation 1)") {
		t.Fatalf("re-apply exit %d, out %q", code, out)
	}

	// Applying the plain manifest drops the placement, and the
	// status line goes away with it.
	plain := writeManifest(t, irisV1)
	code, out, _ = runCLIState(t, state, "apply", "-f", plain)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 2)") {
		t.Fatalf("apply plain exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status exit %d, out %q", code, out)
	}
	if strings.Contains(out, "placement:") {
		t.Fatalf("status %q, want no placement line for an unconstrained deployment", out)
	}
}
