package api

import (
	"strings"
	"testing"
)

func TestValidateMetadata(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ModelDeployment)
		wantErr string // substring of an error, "" means valid
	}{
		{"no metadata", func(d *ModelDeployment) {}, ""},
		{"plain labels", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"team": "search", "cost-center": "ml-platform"}
		}, ""},
		{"prefixed label key", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"example.com/owner": "team-search"}
		}, ""},
		{"empty label value", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"team": ""}
		}, ""},
		{"bad label key", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"Team!": "search"}
		}, "not a valid label key"},
		{"label value too long", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"team": strings.Repeat("a", 64)}
		}, "not a valid label value"},
		{"reserved app.kubernetes.io label", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"app.kubernetes.io/name": "other"}
		}, "reserved prefix"},
		{"reserved ml-operator label", func(d *ModelDeployment) {
			d.Spec.Labels = map[string]string{"ml-operator/team": "search"}
		}, "reserved prefix"},
		{"plain annotations", func(d *ModelDeployment) {
			d.Spec.Annotations = map[string]string{"owner": "team-search", "runbook": "https://example.com/runbooks/iris-mlp"}
		}, ""},
		{"long annotation value", func(d *ModelDeployment) {
			d.Spec.Annotations = map[string]string{"note": strings.Repeat("a", 500)}
		}, ""},
		{"bad annotation key", func(d *ModelDeployment) {
			d.Spec.Annotations = map[string]string{"owner name": "team-search"}
		}, "not a valid annotation key"},
		{"reserved ml-operator annotation", func(d *ModelDeployment) {
			d.Spec.Annotations = map[string]string{"ml-operator/owner": "team-search"}
		}, "reserved prefix"},
		{"reserved app.kubernetes.io annotation", func(d *ModelDeployment) {
			d.Spec.Annotations = map[string]string{"app.kubernetes.io/component": "model"}
		}, "reserved prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDeployment()
			tc.mutate(d)
			errs := Validate(d)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected valid, got errors: %v", errs)
				}
				return
			}
			found := false
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.wantErr) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an error containing %q, got: %v", tc.wantErr, errs)
			}
		})
	}
}

func TestDescribeMetadata(t *testing.T) {
	if got := DescribeMetadata(nil); got != "none" {
		t.Fatalf("nil map: got %q, want %q", got, "none")
	}
	if got := DescribeMetadata(map[string]string{}); got != "none" {
		t.Fatalf("empty map: got %q, want %q", got, "none")
	}
	// Sorted, so the output is stable no matter the manifest order.
	got := DescribeMetadata(map[string]string{"team": "search", "cost-center": "ml-platform"})
	if want := "cost-center=ml-platform, team=search"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
