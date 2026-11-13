package api

import (
	"strings"
	"testing"
)

func int32Ptr(v int32) *int32 { return &v }

func validDeployment() *ModelDeployment {
	return &ModelDeployment{
		Name:      "iris-mlp",
		Namespace: "default",
		Spec: ModelDeploymentSpec{
			Model:    ModelSpec{Image: "registry.example.com/iris-mlp:v2", Framework: "onnx", Version: "v2"},
			Replicas: int32Ptr(2),
			Resources: ResourceRequirements{
				CPURequest: "500m", CPULimit: "2",
				MemoryRequest: "512Mi", MemoryLimit: "1Gi",
			},
			Port:     8080,
			Strategy: StrategyRolling,
			Health:   HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

func TestValidateValid(t *testing.T) {
	if errs := Validate(validDeployment()); errs != nil {
		t.Fatalf("valid deployment rejected: %v", errs)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ModelDeployment)
		want   string
	}{
		{"bad name", func(d *ModelDeployment) { d.Name = "Bad_Name" }, "DNS-1123"},
		{"empty name", func(d *ModelDeployment) { d.Name = "" }, "DNS-1123"},
		{"double dot name", func(d *ModelDeployment) { d.Name = "a..b" }, "DNS-1123"},
		{"empty image", func(d *ModelDeployment) { d.Spec.Model.Image = "  " }, "image must not be empty"},
		{"negative replicas", func(d *ModelDeployment) { d.Spec.Replicas = int32Ptr(-1) }, "replicas must be >= 0"},
		{"port zero", func(d *ModelDeployment) { d.Spec.Port = 0 }, "port must be between"},
		{"port too big", func(d *ModelDeployment) { d.Spec.Port = 70000 }, "port must be between"},
		{"bad cpu", func(d *ModelDeployment) { d.Spec.Resources.CPURequest = "lots" }, "cpuRequest"},
		{"bad memory suffix", func(d *ModelDeployment) { d.Spec.Resources.MemoryLimit = "1Zb" }, "memoryLimit"},
		{"limit below request", func(d *ModelDeployment) {
			d.Spec.Resources.CPURequest = "2"
			d.Spec.Resources.CPULimit = "500m"
		}, "cpuLimit"},
		{"bad strategy", func(d *ModelDeployment) { d.Spec.Strategy = "Teleport" }, "strategy"},
		{"canary without config", func(d *ModelDeployment) { d.Spec.Strategy = StrategyCanary }, "canary is required"},
		{"canary weight zero", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 0}
		}, "canary.weight"},
		{"canary weight 100", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 100}
		}, "canary.weight"},
		{"gate error rate over 1", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 10, Gate: &GateSpec{MaxErrorRate: 1.5}}
		}, "maxErrorRate"},
		{"gate error rate negative", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 10, Gate: &GateSpec{MaxErrorRate: -0.1}}
		}, "maxErrorRate"},
		{"gate latency negative", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 10, Gate: &GateSpec{MaxP95LatencyMs: -5}}
		}, "maxP95LatencyMs"},
		{"gate ready ratio over 1", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyCanary
			d.Spec.Canary = &CanarySpec{Weight: 10, Gate: &GateSpec{MinReadyRatio: 1.2}}
		}, "minReadyRatio"},
		{"autoscaling min over max", func(d *ModelDeployment) {
			d.Spec.Autoscaling = &AutoscalingSpec{MinReplicas: 5, MaxReplicas: 2, TargetCPUUtilization: 80}
		}, "minReplicas"},
		{"autoscaling no target", func(d *ModelDeployment) {
			d.Spec.Autoscaling = &AutoscalingSpec{MinReplicas: 1, MaxReplicas: 3}
		}, "at least one target"},
		{"autoscaling cpu over 100", func(d *ModelDeployment) {
			d.Spec.Autoscaling = &AutoscalingSpec{MinReplicas: 1, MaxReplicas: 3, TargetCPUUtilization: 101}
		}, "targetCPUUtilization"},
		{"bad env key", func(d *ModelDeployment) {
			d.Spec.Env = map[string]string{"1BAD": "x"}
		}, "env key"},
		{"health path no slash", func(d *ModelDeployment) { d.Spec.Health.Path = "healthz" }, "health.path"},
		{"negative health delay", func(d *ModelDeployment) { d.Spec.Health.InitialDelaySeconds = -1 }, "initialDelaySeconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDeployment()
			tc.mutate(d)
			errs := Validate(d)
			if len(errs) == 0 {
				t.Fatalf("expected validation errors, got none")
			}
			joined := ""
			for _, err := range errs {
				joined += err.Error() + "; "
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("expected error containing %q, got %q", tc.want, joined)
			}
		})
	}
}

func TestParseQuantity(t *testing.T) {
	cases := []struct {
		q    string
		cpu  bool
		want int64
	}{
		{"500m", true, 500},
		{"2", true, 2000},
		{"1.5", true, 1500},
		{"512Mi", false, 512 * 1024 * 1024},
		{"1Gi", false, 1024 * 1024 * 1024},
		{"1G", false, 1000 * 1000 * 1000},
		{"256", false, 256},
	}
	for _, tc := range cases {
		got, err := ParseQuantity(tc.q, tc.cpu)
		if err != nil {
			t.Fatalf("ParseQuantity(%q) error: %v", tc.q, err)
		}
		if got != tc.want {
			t.Fatalf("ParseQuantity(%q) = %d, want %d", tc.q, got, tc.want)
		}
	}
	bad := []struct {
		q   string
		cpu bool
	}{
		{"", true}, {"lots", true}, {"500x", true}, {"1Zb", false}, {"-5", true}, {"1.5.2", false},
	}
	for _, tc := range bad {
		if _, err := ParseQuantity(tc.q, tc.cpu); err == nil {
			t.Fatalf("ParseQuantity(%q) should have failed", tc.q)
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	d := &ModelDeployment{
		Name: "minimal",
		Spec: ModelDeploymentSpec{
			Model: ModelSpec{Image: "img:latest"},
		},
	}
	ApplyDefaults(d)
	if d.Namespace != "default" {
		t.Fatalf("namespace = %q, want default", d.Namespace)
	}
	if *d.Spec.Replicas != 1 {
		t.Fatalf("replicas = %d, want 1", *d.Spec.Replicas)
	}
	if d.Spec.Port != 8080 {
		t.Fatalf("port = %d, want 8080", d.Spec.Port)
	}
	if d.Spec.Strategy != StrategyRolling {
		t.Fatalf("strategy = %q, want RollingUpdate", d.Spec.Strategy)
	}
	if d.Spec.Resources.CPURequest != "500m" || d.Spec.Resources.MemoryLimit != "1Gi" {
		t.Fatalf("resources not defaulted: %+v", d.Spec.Resources)
	}
	if d.Spec.Health.Path != "/healthz" {
		t.Fatalf("health path = %q, want /healthz", d.Spec.Health.Path)
	}
	if errs := Validate(d); errs != nil {
		t.Fatalf("defaulted deployment should validate, got %v", errs)
	}

	// Explicit values are never overwritten.
	d2 := validDeployment()
	d2.Spec.Model.Framework = "triton"
	d2.Spec.Health.Path = ""
	ApplyDefaults(d2)
	if d2.Spec.Health.Path != "/v2/health/ready" {
		t.Fatalf("triton health path = %q, want /v2/health/ready", d2.Spec.Health.Path)
	}
	if *d2.Spec.Replicas != 2 || d2.Spec.Port != 8080 {
		t.Fatalf("explicit replicas/port overwritten: %d/%d", *d2.Spec.Replicas, d2.Spec.Port)
	}
}

func TestStatusConditions(t *testing.T) {
	s := ModelDeploymentStatus{}
	if s.ConditionFor("Ready") != nil {
		t.Fatal("expected no Ready condition yet")
	}
	s.SetCondition(Condition{Type: "Ready", Status: "False", Reason: "Provisioning"})
	s.SetCondition(Condition{Type: "Ready", Status: "True", Reason: "AllReplicasReady"})
	if got := s.ConditionFor("Ready"); got == nil || got.Status != "True" {
		t.Fatalf("Ready condition not upserted: %+v", got)
	}
	if len(s.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(s.Conditions))
	}
}

func TestKey(t *testing.T) {
	d := &ModelDeployment{Name: "iris-mlp"}
	if d.Key() != "default/iris-mlp" {
		t.Fatalf("Key() = %q, want default/iris-mlp", d.Key())
	}
	d.Namespace = "prod"
	if d.Key() != "prod/iris-mlp" {
		t.Fatalf("Key() = %q, want prod/iris-mlp", d.Key())
	}
}

func TestApplyDefaultsGateThresholds(t *testing.T) {
	canary := func(gate *GateSpec) *ModelDeployment {
		d := validDeployment()
		d.Spec.Strategy = StrategyCanary
		d.Spec.Canary = &CanarySpec{Weight: 10, Steps: 1, Gate: gate}
		return d
	}

	// No gate at all: the full default set.
	d := canary(nil)
	ApplyDefaults(d)
	if d.Spec.Canary.Gate == nil {
		t.Fatal("nil gate after ApplyDefaults")
	}
	if g := d.Spec.Canary.Gate; g.MaxErrorRate != DefaultGateMaxErrorRate || g.MaxP95LatencyMs != DefaultGateMaxP95LatencyMs || g.MinReadyRatio != DefaultGateMinReadyRatio {
		t.Fatalf("default gate = %+v", g)
	}

	// A partial gate keeps the user's value and fills the rest.
	d = canary(&GateSpec{MaxErrorRate: 0.2})
	ApplyDefaults(d)
	if g := d.Spec.Canary.Gate; g.MaxErrorRate != 0.2 || g.MaxP95LatencyMs != DefaultGateMaxP95LatencyMs || g.MinReadyRatio != DefaultGateMinReadyRatio {
		t.Fatalf("partial gate = %+v", g)
	}

	// A full custom gate is left alone.
	d = canary(&GateSpec{MaxErrorRate: 0.01, MaxP95LatencyMs: 200, MinReadyRatio: 0.5})
	ApplyDefaults(d)
	if g := d.Spec.Canary.Gate; g.MaxErrorRate != 0.01 || g.MaxP95LatencyMs != 200 || g.MinReadyRatio != 0.5 {
		t.Fatalf("custom gate overwritten: %+v", g)
	}

	// A non-canary strategy never gains a gate.
	d = validDeployment()
	ApplyDefaults(d)
	if d.Spec.Canary != nil {
		t.Fatalf("rolling deployment gained a canary spec: %+v", d.Spec.Canary)
	}
}
