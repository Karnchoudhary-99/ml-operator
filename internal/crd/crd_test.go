package crd

import (
	"strings"
	"testing"
)

func TestCRDName(t *testing.T) {
	if got := CRDName(); got != "modeldeployments.ml.example.com" {
		t.Errorf("CRDName() = %q, want modeldeployments.ml.example.com", got)
	}
}

func TestCRDContents(t *testing.T) {
	out, err := CRD()
	if err != nil {
		t.Fatalf("CRD(): %v", err)
	}
	for _, want := range []string{
		"apiVersion: apiextensions.k8s.io/v1",
		"kind: CustomResourceDefinition",
		"name: modeldeployments.ml.example.com",
		"group: ml.example.com",
		"kind: ModelDeployment",
		"listKind: ModelDeploymentList",
		"plural: modeldeployments",
		"singular: modeldeployment",
		"shortNames:",
		"- mldep",
		"scope: Namespaced",
		"name: v1alpha1",
		"served: true",
		"storage: true",
		"subresources:",
		"status: {}",
		"additionalPrinterColumns:",
		"name: Phase",
		"jsonPath: .status.phase",
		"name: Ready",
		"jsonPath: .status.readyReplicas",
		"name: Age",
		"jsonPath: .metadata.creationTimestamp",
		"openAPIV3Schema:",
		"spec:",
		"status:",
		"phase:",
		"Pending",
		"minimum: 1",
		"maximum: 65535",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("CRD YAML missing %q:\n%s", want, out)
		}
	}
	// Exactly one served+stored version: a second version would
	// need a conversion strategy, which the operator does not have.
	if n := strings.Count(out, "storage: true"); n != 1 {
		t.Errorf("CRD has %d storage versions, want 1", n)
	}
}

func TestCRDDeterministic(t *testing.T) {
	a, err := CRD()
	if err != nil {
		t.Fatalf("CRD(): %v", err)
	}
	b, err := CRD()
	if err != nil {
		t.Fatalf("CRD(): %v", err)
	}
	if a != b {
		t.Error("CRD() is not deterministic across calls")
	}
}

func TestValidNamespace(t *testing.T) {
	for _, tc := range []struct {
		ns   string
		want bool
	}{
		{"ml-operator", true},
		{"default", true},
		{"ml.operator", true},
		{"", false},
		{"ML-Operator", false},
		{"has space", false},
		{"-leading", false},
		{"trailing-", false},
	} {
		if got := validNamespace(tc.ns); got != tc.want {
			t.Errorf("validNamespace(%q) = %v, want %v", tc.ns, got, tc.want)
		}
	}
}
