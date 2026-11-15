package crd

import (
	"os"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
)

func TestK8sExampleSpecKeysExistInSchema(t *testing.T) {
	raw, err := os.ReadFile("../../examples/iris-mlp-k8s.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tree, err := manifest.ParseTree(raw)
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	if tree["apiVersion"] != "ml.example.com/v1alpha1" || tree["kind"] != "ModelDeployment" {
		t.Fatalf("example envelope wrong: %v", tree)
	}
	spec, ok := tree["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec is not a map: %T", tree["spec"])
	}
	schema := SpecSchema()
	for k := range spec {
		found := false
		for _, p := range schema.properties {
			if p.name == k {
				found = true
			}
		}
		if !found {
			t.Errorf("examples/iris-mlp-k8s.yaml spec key %q is not in the CRD schema", k)
		}
	}
}
