package crd

import (
	"os"
	"strings"
	"testing"
)

func TestInstallBundleDocuments(t *testing.T) {
	out, err := InstallBundle("", "")
	if err != nil {
		t.Fatalf("InstallBundle: %v", err)
	}
	docs := strings.Split(out, "---\n")
	if len(docs) != 5 {
		t.Fatalf("install bundle has %d documents, want 5 (Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, Deployment)", len(docs))
	}
	kinds := []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Deployment"}
	for i, kind := range kinds {
		if !strings.Contains(docs[i], "kind: "+kind) {
			t.Errorf("document %d should be a %s:\n%s", i, kind, docs[i])
		}
	}
	for _, want := range []string{
		"name: ml-operator-system",
		"kind: ServiceAccount",
		"modeldeployments",
		"modeldeployments/status",
		"horizontalpodautoscalers",
		"image: ghcr.io/karnchoudhary-99/ml-operator:v0.1.0",
		"serviceAccountName: ml-operator",
		"--reconcile-interval=30s",
		"path: /healthz",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install bundle missing %q:\n%s", want, out)
		}
	}
	// The operator must not be granted secrets or node access.
	for _, forbidden := range []string{"secrets", "nodes", "cluster-admin"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("install bundle grants %q, which the reconciler does not need", forbidden)
		}
	}
}

func TestInstallBundleOverrides(t *testing.T) {
	out, err := InstallBundle("custom-ns", "example.com/op:v9")
	if err != nil {
		t.Fatalf("InstallBundle: %v", err)
	}
	if !strings.Contains(out, "name: custom-ns") {
		t.Error("custom namespace not applied to the bundle")
	}
	if !strings.Contains(out, "image: example.com/op:v9") {
		t.Error("custom image not applied to the operator Deployment")
	}
	if strings.Contains(out, "ml-operator-system") {
		t.Error("default namespace leaked into the custom bundle")
	}
}

func TestInstallBundleBadNamespace(t *testing.T) {
	if _, err := InstallBundle("Bad Namespace", ""); err == nil {
		t.Error("InstallBundle accepted an invalid namespace")
	}
}

func TestInstallBundleDeterministic(t *testing.T) {
	a, err := InstallBundle("", "")
	if err != nil {
		t.Fatalf("InstallBundle: %v", err)
	}
	b, err := InstallBundle("", "")
	if err != nil {
		t.Fatalf("InstallBundle: %v", err)
	}
	if a != b {
		t.Error("InstallBundle is not deterministic across calls")
	}
}

// TestGoldenInstallBundle pins the generated bundle to the
// checked-in examples/install.yaml: regenerate it with
// `mlop install > examples/install.yaml` after any change.
func TestGoldenInstallBundle(t *testing.T) {
	want, err := os.ReadFile("../../examples/install.yaml")
	if err != nil {
		t.Fatalf("read examples/install.yaml: %v", err)
	}
	got, err := InstallBundle("", "")
	if err != nil {
		t.Fatalf("InstallBundle: %v", err)
	}
	if got != string(want) {
		t.Error("InstallBundle() differs from examples/install.yaml — regenerate with `mlop install > examples/install.yaml`")
	}
}
