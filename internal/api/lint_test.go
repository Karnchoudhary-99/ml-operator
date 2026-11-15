package api

import (
	"strings"
	"testing"
)

func int32p(n int32) *int32 { return &n }

// lintBase is a clean deployment: pinned tag, framework set, sane
// canary gate. Each test mutates one field and expects one warning.
func lintBase() ModelDeployment {
	return ModelDeployment{
		Name:      "iris-mlp",
		Namespace: "default",
		Spec: ModelDeploymentSpec{
			Model:    ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
			Replicas: int32p(2),
			Port:     8080,
			Strategy: StrategyRolling,
		},
	}
}

func TestLint(t *testing.T) {
	canary := func(weight, interval int32, ratio float64) *CanarySpec {
		return &CanarySpec{Weight: weight, Steps: 1, IntervalSeconds: interval,
			Gate: &GateSpec{MaxErrorRate: 0.05, MaxP95LatencyMs: 500, MinReadyRatio: ratio}}
	}
	cases := []struct {
		name string
		mut  func(*ModelDeployment)
		want string // substring of the expected warning; "" means no warnings
	}{
		{"clean spec", func(d *ModelDeployment) {}, ""},
		{"untagged image", func(d *ModelDeployment) { d.Spec.Model.Image = "registry.example.com/iris-mlp" }, "has no tag"},
		{"latest tag", func(d *ModelDeployment) { d.Spec.Model.Image = "registry.example.com/iris-mlp:latest" }, ":latest tag"},
		{"registry port is not a tag", func(d *ModelDeployment) { d.Spec.Model.Image = "localhost:5000/iris-mlp" }, "has no tag"},
		{"registry port with tag is clean", func(d *ModelDeployment) { d.Spec.Model.Image = "localhost:5000/iris-mlp:v1" }, ""},
		{"empty framework", func(d *ModelDeployment) { d.Spec.Model.Framework = "" }, "framework is empty"},
		{"zero replicas, no autoscaling", func(d *ModelDeployment) { d.Spec.Replicas = int32p(0) }, "replicas is 0"},
		{"zero replicas with autoscaling is fine", func(d *ModelDeployment) {
			d.Spec.Replicas = int32p(0)
			d.Spec.Autoscaling = &AutoscalingSpec{MinReplicas: 1, MaxReplicas: 4, TargetCPUUtilization: 70}
		}, ""},
		{"autoscaling min zero", func(d *ModelDeployment) {
			d.Spec.Autoscaling = &AutoscalingSpec{MinReplicas: 0, MaxReplicas: 4, TargetCPUUtilization: 70}
		}, "minReplicas is 0"},
		{"canary weight over half", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = canary(80, 30, 1.0)
		}, "weight is 80%"},
		{"canary weight at half is fine", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = canary(50, 30, 1.0)
		}, ""},
		{"canary interval over ten minutes", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = canary(10, 900, 1.0)
		}, "intervalSeconds is 900s"},
		{"canary gate ready ratio below one", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = canary(10, 30, 0.5)
		}, "minReadyRatio is 0.5"},
		{"blue-green gate ready ratio below one", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyBlueGreen
			d.Spec.BlueGreen = &BlueGreenSpec{Gate: &GateSpec{MaxErrorRate: 0.05, MaxP95LatencyMs: 500, MinReadyRatio: 0.8}}
		}, "blueGreen.gate.minReadyRatio is 0.8"},
		{"gate ratio zero means default, not a warning", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = canary(10, 30, 0)
		}, ""},
		{"empty image is a validation error, not a lint warning", func(d *ModelDeployment) {
			d.Spec.Model.Image = ""
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := lintBase()
			tc.mut(&d)
			warns := Lint(&d)
			if tc.want == "" {
				if len(warns) != 0 {
					t.Fatalf("Lint = %v, want none", warns)
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
				t.Fatalf("Lint = %v, want a warning containing %q", warns, tc.want)
			}
		})
	}
}

func TestLintMultipleWarnings(t *testing.T) {
	// Several risky choices at once all surface; lint never stops
	// at the first problem the way validation short-circuits.
	d := lintBase()
	d.Spec.Model.Image = "iris-mlp:latest"
	d.Spec.Model.Framework = ""
	d.Spec.Replicas = int32p(0)
	warns := Lint(&d)
	if len(warns) != 3 {
		t.Fatalf("Lint = %v, want 3 warnings", warns)
	}
}
