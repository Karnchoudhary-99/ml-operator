package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCRDCommand(t *testing.T) {
	code, out, _ := runCLI(t, "crd")
	if code != 0 {
		t.Fatalf("crd exit %d", code)
	}
	for _, want := range []string{
		"kind: CustomResourceDefinition",
		"modeldeployments.ml.example.com",
		"openAPIV3Schema:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("crd output missing %q", want)
		}
	}
	// crd never creates a state file.
	if _, err := os.Stat(filepath.Join(t.TempDir(), "state.json")); !os.IsNotExist(err) {
		t.Error("crd created a state file")
	}
}

func TestCRDCommandRejectsArgs(t *testing.T) {
	code, _, _ := runCLI(t, "crd", "extra")
	if code == 0 {
		t.Error("crd with an argument should fail")
	}
}

func TestInstallCommand(t *testing.T) {
	code, out, _ := runCLI(t, "install")
	if code != 0 {
		t.Fatalf("install exit %d", code)
	}
	for _, want := range []string{
		"kind: Namespace",
		"kind: ClusterRole",
		"kind: Deployment",
		"name: ml-operator-system",
		"ghcr.io/karnchoudhary-99/ml-operator:v0.1.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install output missing %q", want)
		}
	}
}

func TestInstallCommandOverrides(t *testing.T) {
	code, out, _ := runCLI(t, "install", "--namespace", "custom-ns", "--image", "example.com/op:v9")
	if code != 0 {
		t.Fatalf("install --namespace/--image exit %d", code)
	}
	if !strings.Contains(out, "name: custom-ns") || !strings.Contains(out, "image: example.com/op:v9") {
		t.Errorf("install overrides not applied:\n%s", out)
	}
}

func TestInstallCommandBadNamespace(t *testing.T) {
	code, _, errOut := runCLI(t, "install", "--namespace", "Bad Namespace")
	if code == 0 || !strings.Contains(errOut, "must be a DNS-1123 subdomain") {
		t.Fatalf("install bad namespace exit %d, err %q", code, errOut)
	}
}
