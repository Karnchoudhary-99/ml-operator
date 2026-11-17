package api

import (
	"strings"
	"testing"
)

func TestValidatePlacement(t *testing.T) {
	secs := int64(300)
	cases := []struct {
		name    string
		p       *PlacementSpec
		wantErr string // substring of an error, "" means valid
	}{
		{"nil placement is valid", nil, ""},
		{"empty placement is valid", &PlacementSpec{}, ""},
		{"nodeSelector with a plain label", &PlacementSpec{NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"}}, ""},
		{"nodeSelector with a prefixed label", &PlacementSpec{NodeSelector: map[string]string{"node.kubernetes.io/instance-type": "g4dn.xlarge"}}, ""},
		{"nodeSelector key with a space is invalid", &PlacementSpec{NodeSelector: map[string]string{"bad key": "x"}}, "not a valid label key"},
		{"nodeSelector key with a bad prefix is invalid", &PlacementSpec{NodeSelector: map[string]string{"bad_prefix/x": "y"}}, "not a valid label key"},
		{"nodeSelector value with a slash is invalid", &PlacementSpec{NodeSelector: map[string]string{"k": "a/b"}}, "not a valid label value"},
		{"nodeSelector value over 63 chars is invalid", &PlacementSpec{NodeSelector: map[string]string{"k": strings.Repeat("a", 64)}}, "not a valid label value"},
		{"Equal toleration with key and value", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Operator: "Equal", Value: "true", Effect: "NoSchedule"}}}, ""},
		{"empty operator means Equal", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Value: "true", Effect: "NoSchedule"}}}, ""},
		{"Exists toleration with no value", &PlacementSpec{Tolerations: []Toleration{{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &secs}}}, ""},
		{"Exists toleration with an empty key matches everything", &PlacementSpec{Tolerations: []Toleration{{Operator: "Exists", Effect: "NoSchedule"}}}, ""},
		{"unknown operator is invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Operator: "Maybe", Value: "true"}}}, "must be \"Equal\" or \"Exists\""},
		{"Exists toleration with a value is invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Operator: "Exists", Value: "true"}}}, "must be empty when operator is \"Exists\""},
		{"Equal toleration with an empty key is invalid", &PlacementSpec{Tolerations: []Toleration{{Operator: "Equal", Value: "true"}}}, "must not be empty when operator is \"Equal\""},
		{"unknown effect is invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Effect: "Never"}}}, "must be \"NoSchedule\", \"PreferNoSchedule\", \"NoExecute\""},
		{"seconds with NoSchedule is invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Effect: "NoSchedule", TolerationSeconds: &secs}}}, "only meaningful with effect \"NoExecute\""},
		{"negative seconds are invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Effect: "NoExecute", TolerationSeconds: int64Ptr(-1)}}}, "must be >= 0"},
		{"empty effect with seconds is invalid", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", TolerationSeconds: &secs}}}, "only meaningful with effect \"NoExecute\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDeployment()
			d.Spec.Placement = tc.p
			errs := Validate(d)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("Validate() = %v, want no errors", errs)
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
				t.Fatalf("Validate() = %v, want an error containing %q", errs, tc.wantErr)
			}
		})
	}
}

func TestLintPlacement(t *testing.T) {
	secs := int64(300)
	cases := []struct {
		name string
		p    *PlacementSpec
		want string // warning substring, "" means no warnings from placement
	}{
		{"nil placement is clean", nil, ""},
		{"plain placement is clean", &PlacementSpec{
			NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"},
			Tolerations:  []Toleration{{Key: "gpu", Value: "true", Effect: "NoSchedule"}},
		}, ""},
		{"NoExecute with a grace period is clean", &PlacementSpec{
			Tolerations: []Toleration{{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &secs}},
		}, ""},
		{"empty-key Exists toleration warns", &PlacementSpec{
			Tolerations: []Toleration{{Operator: "Exists", Effect: "NoSchedule"}},
		}, "tolerates every taint"},
		{"NoExecute without seconds warns", &PlacementSpec{
			Tolerations: []Toleration{{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"}},
		}, "evicted immediately"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warns := lintPlacement(tc.p)
			if tc.want == "" {
				if len(warns) != 0 {
					t.Fatalf("lintPlacement() = %v, want no warnings", warns)
				}
				return
			}
			found := false
			for _, w := range warns {
				if strings.Contains(w, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("lintPlacement() = %v, want a warning containing %q", warns, tc.want)
			}
		})
	}
}

func TestPlacementDescribe(t *testing.T) {
	cases := []struct {
		name string
		p    *PlacementSpec
		want string
	}{
		{"nil is unconstrained", nil, "unconstrained"},
		{"empty is unconstrained", &PlacementSpec{}, "unconstrained"},
		{"selector only", &PlacementSpec{NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"}}, "nodeSelector accelerator=nvidia-tesla-t4"},
		{"selector keys sort", &PlacementSpec{NodeSelector: map[string]string{"zone": "a", "accelerator": "t4"}}, "nodeSelector accelerator=t4,zone=a"},
		{"toleration only", &PlacementSpec{Tolerations: []Toleration{{Key: "gpu", Value: "true", Effect: "NoSchedule"}}}, "tolerations gpu=true:NoSchedule"},
		{"both halves", &PlacementSpec{
			NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"},
			Tolerations:  []Toleration{{Key: "gpu", Value: "true", Effect: "NoSchedule"}},
		}, "nodeSelector accelerator=nvidia-tesla-t4, tolerations gpu=true:NoSchedule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Describe(); got != tc.want {
				t.Fatalf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTolerationString(t *testing.T) {
	secs := int64(300)
	cases := []struct {
		name string
		tol  Toleration
		want string
	}{
		{"equal with value and effect", Toleration{Key: "gpu", Value: "true", Effect: "NoSchedule"}, "gpu=true:NoSchedule"},
		{"equal with value, no effect", Toleration{Key: "gpu", Value: "true"}, "gpu=true"},
		{"exists with effect", Toleration{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &secs}, "dedicated:Exists:NoExecute:300"},
		{"exists with empty key", Toleration{Operator: "Exists", Effect: "NoSchedule"}, ":Exists:NoSchedule"},
		{"equal with empty value", Toleration{Key: "gpu", Effect: "NoSchedule"}, "gpu:NoSchedule"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tol.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
