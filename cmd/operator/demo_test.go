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
