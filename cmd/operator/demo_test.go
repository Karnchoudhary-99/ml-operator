package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunDemo(t *testing.T) {
	var buf bytes.Buffer
	if err := runDemo(&buf); err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"registered iris-mlp v1",
		"CreateDeployment default/iris-mlp",
		"CreateService default/iris-mlp",
		"phase=Progressing",
		"phase=Available",
		"UpdateDeployment default/iris-mlp",
		`image "registry.example.com/iris-mlp:v1" -> "registry.example.com/iris-mlp:v2"`,
		"registered iris-mlp v3 (canary 10%, 2 steps)",
		"CreateDeployment default/iris-mlp-canary",
		"canary advanced to 55%",
		"canary advanced to 100%",
		"canary promoted",
		"DeleteDeployment default/iris-mlp-canary",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("demo output missing %q:\n%s", want, out)
		}
	}
	// The demo ends Available: the last reconcile reports it.
	if !strings.HasSuffix(strings.TrimSpace(out), "live state matches desired state)") {
		t.Fatalf("demo does not end on an in-sync NoOp:\n%s", out)
	}
}
