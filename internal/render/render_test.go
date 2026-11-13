package render

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func dep() api.ModelDeployment {
	d := api.ModelDeployment{
		Name:       "iris-mlp",
		Namespace:  "default",
		Generation: 2,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v2", Framework: "onnx", Version: "v2"},
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyRolling,
			Env:       map[string]string{"BATCH_SIZE": "32", "LOG_LEVEL": "info"},
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
	replicas := int32(3)
	d.Spec.Replicas = &replicas
	api.ApplyDefaults(&d)
	return d
}

func TestDeploymentFor(t *testing.T) {
	d := dep()
	got := DeploymentFor(d)

	if got.Name != "iris-mlp" || got.Namespace != "default" {
		t.Fatalf("identity wrong: %+v", got)
	}
	if got.Replicas != 3 {
		t.Fatalf("replicas = %d, want 3", got.Replicas)
	}
	if got.Canary {
		t.Fatal("stable Deployment must not be marked canary")
	}
	c := got.Container
	if c.Name != "model" || c.Image != "registry.example.com/iris-mlp:v2" || c.Port != 8080 {
		t.Fatalf("container wrong: %+v", c)
	}
	if c.CPURequest != "500m" || c.CPULimit != "1" || c.MemoryRequest != "512Mi" || c.MemoryLimit != "1Gi" {
		t.Fatalf("resources wrong: %+v", c)
	}
	if c.ReadinessProbe != (Probe{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10}) {
		t.Fatalf("probe wrong: %+v", c.ReadinessProbe)
	}
	// Env is sorted by name, so rendering is deterministic.
	if len(c.Env) != 2 || c.Env[0].Name != "BATCH_SIZE" || c.Env[1].Name != "LOG_LEVEL" {
		t.Fatalf("env not sorted: %+v", c.Env)
	}
	// The selector must not carry the version label: a version bump
	// must never change the selector (it would orphan pods).
	if _, ok := got.Selector["ml-operator/version"]; ok {
		t.Fatal("selector carries the version label")
	}
	if got.Labels["ml-operator/version"] != "v2" {
		t.Fatalf("pod labels missing version: %v", got.Labels)
	}
	if got.Labels["ml-operator/managed-by"] != ManagedBy {
		t.Fatalf("managed-by label wrong: %v", got.Labels)
	}
}

func TestServiceFor(t *testing.T) {
	d := dep()
	got := ServiceFor(d)
	if got.Name != "iris-mlp" || got.Port != 80 || got.TargetPort != 8080 {
		t.Fatalf("service wrong: %+v", got)
	}
	// The Service selects stable pods only — same selector as the
	// stable Deployment, no version, no canary label.
	if got.Selector["app.kubernetes.io/name"] != "iris-mlp" {
		t.Fatalf("service selector wrong: %v", got.Selector)
	}
	if _, ok := got.Selector["ml-operator/canary"]; ok {
		t.Fatal("service selector matches canary pods")
	}
}

func TestCanaryDeploymentFor(t *testing.T) {
	d := dep()
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 25, Steps: 4, IntervalSeconds: 30}
	api.ApplyDefaults(&d)

	got := CanaryDeploymentFor(d)
	if got.Name != "iris-mlp-canary" || !got.Canary {
		t.Fatalf("canary identity wrong: %+v", got)
	}
	// 25% of 3 replicas rounds to 1.
	if got.Replicas != 1 {
		t.Fatalf("canary replicas = %d, want 1", got.Replicas)
	}
	if got.Labels["ml-operator/canary"] != "true" {
		t.Fatalf("canary label missing: %v", got.Labels)
	}
	if got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("canary image wrong: %s", got.Container.Image)
	}
	// The canary selector must not collide with the stable one: the
	// Service must never route to canary pods by accident.
	if got.Selector["ml-operator/canary"] != "true" {
		t.Fatalf("canary selector missing canary label: %v", got.Selector)
	}

	// 50% of 4 replicas is 2; a weight with no canary spec falls
	// back to 10%.
	d2 := dep()
	replicas := int32(4)
	d2.Spec.Replicas = &replicas
	if got := CanaryDeploymentFor(d2); got.Replicas != 1 {
		t.Fatalf("no-canary-spec weight fallback: replicas = %d, want 1 (10%% of 4 rounds to 1)", got.Replicas)
	}
	d2.Spec.Canary = &api.CanarySpec{Weight: 50}
	if got := CanaryDeploymentFor(d2); got.Replicas != 2 {
		t.Fatalf("50%% canary of 4: replicas = %d, want 2", got.Replicas)
	}
}

func TestDeploymentDiffReasons(t *testing.T) {
	desired := DeploymentFor(dep())
	if reasons := DeploymentDiffReasons(desired, desired); len(reasons) != 0 {
		t.Fatalf("identical deployments differ: %v", reasons)
	}

	live := DeploymentFor(dep())
	live.Replicas = 1
	live.Container.Image = "registry.example.com/iris-mlp:v1"
	live.Container.Env = []EnvVar{{Name: "LOG_LEVEL", Value: "debug"}}
	live.Container.CPULimit = "2"
	live.Container.ReadinessProbe.Path = "/ready"
	reasons := DeploymentDiffReasons(desired, live)
	if len(reasons) != 5 {
		t.Fatalf("reasons = %v, want 5 (replicas, image, env, resources, probe)", reasons)
	}

	// Resources-only change is a single reason.
	live2 := DeploymentFor(dep())
	live2.Container.MemoryLimit = "2Gi"
	if reasons := DeploymentDiffReasons(desired, live2); len(reasons) != 1 || reasons[0] != "resources changed" {
		t.Fatalf("resources-only diff = %v", reasons)
	}

	// Service diff: port and targetPort.
	svc := ServiceFor(dep())
	liveSvc := svc
	liveSvc.TargetPort = 9000
	if reasons := ServiceDiffReasons(svc, liveSvc); len(reasons) != 1 {
		t.Fatalf("service diff = %v, want 1 (targetPort)", reasons)
	}
	if reasons := ServiceDiffReasons(svc, svc); len(reasons) != 0 {
		t.Fatalf("identical services differ: %v", reasons)
	}
}
