package api

import (
	"strings"
	"testing"
)

// TestDescribeImagePullSecrets pins the status rendering:
// names joined with ", ", an empty list reading "none".
func TestDescribeImagePullSecrets(t *testing.T) {
	if got := DescribeImagePullSecrets(nil); got != "none" {
		t.Fatalf("nil = %q, want %q", got, "none")
	}
	if got := DescribeImagePullSecrets([]string{}); got != "none" {
		t.Fatalf("empty = %q, want %q", got, "none")
	}
	if got := DescribeImagePullSecrets([]string{"regcred"}); got != "regcred" {
		t.Fatalf("one = %q, want %q", got, "regcred")
	}
	if got := DescribeImagePullSecrets([]string{"regcred", "ghcr-creds"}); got != "regcred, ghcr-creds" {
		t.Fatalf("two = %q, want %q", got, "regcred, ghcr-creds")
	}
}

// TestValidateImagePullSecrets pins the admission rules: an
// empty list is valid, plain names pass, an empty name, a name
// that is not a DNS-1123 subdomain, and a duplicate are all
// rejected.
func TestValidateImagePullSecrets(t *testing.T) {
	base := func() *ModelDeployment {
		return &ModelDeployment{
			Name:      "iris-mlp",
			Namespace: "default",
			Spec: ModelDeploymentSpec{
				Model:    ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
				Port:     8080,
				Strategy: StrategyRolling,
				Health:   HealthSpec{Path: "/healthz"},
			},
		}
	}
	if errs := Validate(base()); errs != nil {
		t.Fatalf("empty imagePullSecrets rejected: %v", errs)
	}
	d := base()
	d.Spec.ImagePullSecrets = []string{"regcred", "ghcr-creds"}
	if errs := Validate(d); errs != nil {
		t.Fatalf("valid imagePullSecrets rejected: %v", errs)
	}
	cases := []struct {
		name    string
		secrets []string
		want    string
	}{
		{"empty name", []string{"regcred", ""}, "must not be empty"},
		{"whitespace name", []string{"  "}, "must not be empty"},
		{"uppercase name", []string{"RegCred"}, "must be a DNS-1123 subdomain"},
		{"underscore name", []string{"reg_cred"}, "must be a DNS-1123 subdomain"},
		{"duplicate", []string{"regcred", "ghcr-creds", "regcred"}, "duplicates secret"},
	}
	for _, c := range cases {
		d := base()
		d.Spec.ImagePullSecrets = c.secrets
		errs := Validate(d)
		if len(errs) == 0 {
			t.Fatalf("%s: expected a validation error, got none", c.name)
		}
		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), c.want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: errors %v, want one containing %q", c.name, errs, c.want)
		}
	}
}
