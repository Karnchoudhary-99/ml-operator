package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
		{"blue-green gate error rate over 1", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyBlueGreen
			d.Spec.BlueGreen = &BlueGreenSpec{Gate: &GateSpec{MaxErrorRate: 1.5}}
		}, "blueGreen.gate.maxErrorRate"},
		{"blue-green gate latency negative", func(d *ModelDeployment) {
			d.Spec.Strategy = StrategyBlueGreen
			d.Spec.BlueGreen = &BlueGreenSpec{Gate: &GateSpec{MaxP95LatencyMs: -5}}
		}, "blueGreen.gate.maxP95LatencyMs"},
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
		{"availability minAvailable negative", func(d *ModelDeployment) {
			d.Spec.Availability = &AvailabilitySpec{MinAvailable: -1}
		}, "minAvailable"},
		{"availability minAvailable equals replicas", func(d *ModelDeployment) {
			d.Spec.Availability = &AvailabilitySpec{MinAvailable: 2}
		}, "minAvailable (2) must be < replicas (2)"},
		{"availability minAvailable above replicas", func(d *ModelDeployment) {
			d.Spec.Availability = &AvailabilitySpec{MinAvailable: 5}
		}, "minAvailable (5) must be < replicas (2)"},
		{"availability maxSkew negative", func(d *ModelDeployment) {
			d.Spec.Availability = &AvailabilitySpec{MaxSkew: -1}
		}, "maxSkew"},
		{"availability maxSkew too large", func(d *ModelDeployment) {
			d.Spec.Availability = &AvailabilitySpec{MaxSkew: 11}
		}, "maxSkew"},
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

// TestAvailabilityEffectiveValues pins the "zero means default"
// resolution: a nil spec and a zero spec both resolve to
// replicas-1 / anti-affinity on / maxSkew 1, and a single replica
// has an effective minAvailable of 0 (nothing to protect).
func TestAvailabilityEffectiveValues(t *testing.T) {
	var nilSpec *AvailabilitySpec
	if got := nilSpec.EffectiveMinAvailable(3); got != 2 {
		t.Fatalf("nil EffectiveMinAvailable(3) = %d, want 2", got)
	}
	if !nilSpec.AntiAffinityEnabled() {
		t.Fatal("nil AntiAffinityEnabled() = false, want true")
	}
	if got := nilSpec.EffectiveMaxSkew(); got != 1 {
		t.Fatalf("nil EffectiveMaxSkew() = %d, want 1", got)
	}
	if got := nilSpec.EffectiveMinAvailable(1); got != 0 {
		t.Fatalf("nil EffectiveMinAvailable(1) = %d, want 0", got)
	}

	zero := &AvailabilitySpec{}
	if got := zero.EffectiveMinAvailable(4); got != 3 {
		t.Fatalf("zero EffectiveMinAvailable(4) = %d, want 3", got)
	}
	explicit := &AvailabilitySpec{MinAvailable: 1, AntiAffinity: boolPtr(false), MaxSkew: 2}
	if got := explicit.EffectiveMinAvailable(4); got != 1 {
		t.Fatalf("explicit EffectiveMinAvailable(4) = %d, want 1", got)
	}
	if explicit.AntiAffinityEnabled() {
		t.Fatal("explicit AntiAffinityEnabled() = true, want false")
	}
	if got := explicit.EffectiveMaxSkew(); got != 2 {
		t.Fatalf("explicit EffectiveMaxSkew() = %d, want 2", got)
	}
}

// TestApplyDefaultsAvailability pins the defaulting contract: a
// nil availability block takes the full default set, and a partial
// block keeps the user's minAvailable while the anti-affinity flag
// fills in — setting minAvailable must not silently switch
// anti-affinity off.
func TestApplyDefaultsAvailability(t *testing.T) {
	d := &ModelDeployment{
		Name: "minimal",
		Spec: ModelDeploymentSpec{Model: ModelSpec{Image: "img:latest"}},
	}
	ApplyDefaults(d)
	if d.Spec.Availability == nil {
		t.Fatal("nil availability block was not defaulted")
	}
	if !d.Spec.Availability.AntiAffinityEnabled() {
		t.Fatal("defaulted anti-affinity is off, want on")
	}
	if d.Spec.Availability.MinAvailable != 0 || d.Spec.Availability.MaxSkew != 0 {
		t.Fatalf("defaulted availability = %+v, want zero minAvailable/maxSkew", d.Spec.Availability)
	}

	d2 := validDeployment()
	d2.Spec.Availability = &AvailabilitySpec{MinAvailable: 1}
	ApplyDefaults(d2)
	if d2.Spec.Availability.MinAvailable != 1 {
		t.Fatalf("explicit minAvailable overwritten: %d", d2.Spec.Availability.MinAvailable)
	}
	if !d2.Spec.Availability.AntiAffinityEnabled() {
		t.Fatal("partial availability block lost its anti-affinity default")
	}
	if errs := Validate(d2); errs != nil {
		t.Fatalf("defaulted availability deployment should validate, got %v", errs)
	}
}

// TestSecurityEffectiveValues pins the "nil means hardened
// default" resolution: a nil spec resolves to non-root uid 10001,
// read-only rootfs, no privilege escalation, drop ALL,
// RuntimeDefault seccomp — and an explicit relaxation resolves
// to exactly what the author asked for.
func TestSecurityEffectiveValues(t *testing.T) {
	var nilSpec *SecuritySpec
	if !nilSpec.RunAsNonRootEnabled() {
		t.Fatal("nil RunAsNonRootEnabled() = false, want true")
	}
	if got := nilSpec.EffectiveRunAsUser(); got != 10001 {
		t.Fatalf("nil EffectiveRunAsUser() = %d, want 10001", got)
	}
	if !nilSpec.ReadOnlyRootFilesystemEnabled() {
		t.Fatal("nil ReadOnlyRootFilesystemEnabled() = false, want true")
	}
	if nilSpec.AllowPrivilegeEscalationEnabled() {
		t.Fatal("nil AllowPrivilegeEscalationEnabled() = true, want false")
	}
	if got := nilSpec.EffectiveDropCapabilities(); len(got) != 1 || got[0] != "ALL" {
		t.Fatalf("nil EffectiveDropCapabilities() = %v, want [ALL]", got)
	}
	if got := nilSpec.EffectiveSeccompProfile(); got != "RuntimeDefault" {
		t.Fatalf("nil EffectiveSeccompProfile() = %q, want RuntimeDefault", got)
	}

	relaxed := &SecuritySpec{
		RunAsNonRoot:             boolPtr(false),
		RunAsUser:                int64Ptr(0),
		ReadOnlyRootFilesystem:   boolPtr(false),
		AllowPrivilegeEscalation: boolPtr(true),
		DropCapabilities:         []string{"NET_BIND_SERVICE"},
		SeccompProfile:           "Unconfined",
	}
	if relaxed.RunAsNonRootEnabled() || relaxed.ReadOnlyRootFilesystemEnabled() || !relaxed.AllowPrivilegeEscalationEnabled() {
		t.Fatal("relaxed spec did not resolve to its explicit values")
	}
	if got := relaxed.EffectiveRunAsUser(); got != 0 {
		t.Fatalf("relaxed EffectiveRunAsUser() = %d, want 0", got)
	}
	if got := relaxed.EffectiveDropCapabilities(); len(got) != 1 || got[0] != "NET_BIND_SERVICE" {
		t.Fatalf("relaxed EffectiveDropCapabilities() = %v, want [NET_BIND_SERVICE]", got)
	}
	// The returned list is a copy: mutating it must not change
	// the spec (the render path passes it straight through).
	got := relaxed.EffectiveDropCapabilities()
	got[0] = "CHOWN"
	if relaxed.DropCapabilities[0] != "NET_BIND_SERVICE" {
		t.Fatal("EffectiveDropCapabilities returned the spec's own slice")
	}
}

// TestApplyDefaultsSecurity pins the defaulting contract: a nil
// security block takes the full hardened set, and a partial block
// keeps the user's runAsUser while the other fields fill in —
// setting one field must not silently relax the rest.
func TestApplyDefaultsSecurity(t *testing.T) {
	d := &ModelDeployment{
		Name: "minimal",
		Spec: ModelDeploymentSpec{Model: ModelSpec{Image: "img:latest"}},
	}
	ApplyDefaults(d)
	if d.Spec.Security == nil {
		t.Fatal("nil security block was not defaulted")
	}
	s := d.Spec.Security
	if !s.RunAsNonRootEnabled() || s.EffectiveRunAsUser() != 10001 ||
		!s.ReadOnlyRootFilesystemEnabled() || s.AllowPrivilegeEscalationEnabled() ||
		s.EffectiveSeccompProfile() != "RuntimeDefault" {
		t.Fatalf("defaulted security = %+v, want the hardened set", s)
	}
	if got := s.EffectiveDropCapabilities(); len(got) != 1 || got[0] != "ALL" {
		t.Fatalf("defaulted dropCapabilities = %v, want [ALL]", got)
	}
	if errs := Validate(d); errs != nil {
		t.Fatalf("defaulted security deployment should validate, got %v", errs)
	}

	d2 := validDeployment()
	uid := int64(20000)
	d2.Spec.Security = &SecuritySpec{RunAsUser: &uid}
	ApplyDefaults(d2)
	if d2.Spec.Security.EffectiveRunAsUser() != 20000 {
		t.Fatalf("explicit runAsUser overwritten: %d", d2.Spec.Security.EffectiveRunAsUser())
	}
	if !d2.Spec.Security.RunAsNonRootEnabled() || !d2.Spec.Security.ReadOnlyRootFilesystemEnabled() {
		t.Fatal("partial security block lost its hardening defaults")
	}
	if errs := Validate(d2); errs != nil {
		t.Fatalf("defaulted partial security deployment should validate, got %v", errs)
	}
}

// TestValidateSecurity pins the rejection rules: uid 0 with
// runAsNonRoot on is a contradiction, out-of-range uids and
// malformed capability names are nonsense, and a seccomp profile
// that is not RuntimeDefault / Unconfined / Localhost/* is not
// a profile the kubelet knows. A relaxed-but-consistent spec
// (root with runAsNonRoot off) validates — taste is Lint's job.
func TestValidateSecurity(t *testing.T) {
	uid0 := int64(0)
	root := validDeployment()
	root.Spec.Security = &SecuritySpec{RunAsNonRoot: boolPtr(true), RunAsUser: &uid0}
	if errs := Validate(root); len(errs) == 0 {
		t.Fatal("uid 0 with runAsNonRoot on should not validate")
	}
	// Same uid with runAsNonRoot explicitly off is consistent.
	root.Spec.Security.RunAsNonRoot = boolPtr(false)
	if errs := Validate(root); errs != nil {
		t.Fatalf("root with runAsNonRoot off should validate, got %v", errs)
	}

	badUID := int64(-1)
	d := validDeployment()
	d.Spec.Security = &SecuritySpec{RunAsUser: &badUID}
	if errs := Validate(d); len(errs) == 0 {
		t.Fatal("negative runAsUser should not validate")
	}

	d2 := validDeployment()
	d2.Spec.Security = &SecuritySpec{DropCapabilities: []string{"net_bind_service"}}
	if errs := Validate(d2); len(errs) == 0 {
		t.Fatal("lowercase capability name should not validate")
	}

	d3 := validDeployment()
	d3.Spec.Security = &SecuritySpec{SeccompProfile: "MyProfile"}
	if errs := Validate(d3); len(errs) == 0 {
		t.Fatal("unknown seccomp profile should not validate")
	}
	d3.Spec.Security.SeccompProfile = "Localhost/my-profile.json"
	if errs := Validate(d3); errs != nil {
		t.Fatalf("Localhost seccomp profile should validate, got %v", errs)
	}
}

// TestLintSecurity pins the warnings: an unset block is clean,
// and every explicit relaxation warns by name. Lint runs on the
// manifest as written, before defaults fill anything in.
func TestLintSecurity(t *testing.T) {
	d := validDeployment()
	if warns := Lint(d); len(warns) != 0 {
		t.Fatalf("deployment without a security block should be lint-clean, got %v", warns)
	}
	d.Spec.Security = &SecuritySpec{
		RunAsNonRoot:             boolPtr(false),
		RunAsUser:                int64Ptr(0),
		ReadOnlyRootFilesystem:   boolPtr(false),
		AllowPrivilegeEscalation: boolPtr(true),
		DropCapabilities:         []string{},
		SeccompProfile:           "Unconfined",
	}
	warns := Lint(d)
	if len(warns) != 6 {
		t.Fatalf("fully relaxed security should warn 6 times, got %d: %v", len(warns), warns)
	}
	for _, want := range []string{"runAsNonRoot", "runAsUser", "readOnlyRootFilesystem", "allowPrivilegeEscalation", "dropCapabilities", "seccompProfile"} {
		found := false
		for _, w := range warns {
			if strings.Contains(w, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no warning mentions %s: %v", want, warns)
		}
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

// TestSetConditionTransitionTimes pins the Kubernetes rules: a new
// condition is stamped, a same-status update keeps the old stamp
// (only Reason/Message move), and a status flip re-stamps.
func TestSetConditionTransitionTimes(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Minute)
	t2 := t1.Add(5 * time.Minute)

	s := ModelDeploymentStatus{}
	s.SetConditionAt(Condition{Type: "Ready", Status: "False", Reason: "Provisioning", Message: "0/1 ready"}, t0)
	got := s.ConditionFor("Ready")
	if got == nil || !got.LastTransitionTime.Equal(t0) {
		t.Fatalf("new condition not stamped with t0: %+v", got)
	}

	// Same status, new reason/message: the stamp must not move.
	s.SetConditionAt(Condition{Type: "Ready", Status: "False", Reason: "Provisioning", Message: "still 0/1 ready"}, t1)
	if got := s.ConditionFor("Ready"); !got.LastTransitionTime.Equal(t0) {
		t.Fatalf("same-status update moved the stamp: %v, want %v", got.LastTransitionTime, t0)
	} else if got.Message != "still 0/1 ready" {
		t.Fatalf("same-status update did not refresh the message: %q", got.Message)
	}

	// Status flip: re-stamped with the flip time.
	s.SetConditionAt(Condition{Type: "Ready", Status: "True", Reason: "ReplicasAvailable", Message: "1/1 ready"}, t2)
	if got := s.ConditionFor("Ready"); !got.LastTransitionTime.Equal(t2) {
		t.Fatalf("status flip not re-stamped: %v, want %v", got.LastTransitionTime, t2)
	}
	if len(s.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(s.Conditions))
	}
}

// TestSetConditionZeroTimestampStamped covers state files written
// before transition times existed: a stored condition with a zero
// stamp is stamped on first sight instead of keeping the zero value.
func TestSetConditionZeroTimestampStamped(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	s := ModelDeploymentStatus{Conditions: []Condition{
		{Type: "Ready", Status: "True", Reason: "ReplicasAvailable"},
	}}
	s.SetConditionAt(Condition{Type: "Ready", Status: "True", Reason: "ReplicasAvailable", Message: "1/1 ready"}, t0)
	if got := s.ConditionFor("Ready"); got.LastTransitionTime.IsZero() {
		t.Fatal("zero-stamp condition was not stamped on first sight")
	} else if !got.LastTransitionTime.Equal(t0) {
		t.Fatalf("zero-stamp condition stamped %v, want %v", got.LastTransitionTime, t0)
	}
}

// TestConditionJSONRoundTrip pins the wire form: the transition time
// survives a JSON round-trip (state file / `mlop status --json`).
// A zero time is serialized as the RFC 3339 zero value, never
// dropped: encoding/json omitempty does not omit struct values, so
// the field is always present and an absent timestamp reads as
// "unknown" rather than a missing key.
func TestConditionJSONRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	s := ModelDeploymentStatus{}
	s.SetConditionAt(Condition{Type: "Ready", Status: "True", Reason: "ReplicasAvailable"}, t0)
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var back ModelDeploymentStatus
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if got := back.ConditionFor("Ready"); got == nil || !got.LastTransitionTime.Equal(t0) {
		t.Fatalf("transition time lost in JSON round-trip: %+v", got)
	}

	raw, err = json.Marshal(Condition{Type: "Ready", Status: "False"})
	if err != nil {
		t.Fatalf("marshal condition: %v", err)
	}
	if !strings.Contains(string(raw), `"lastTransitionTime":"0001-01-01T00:00:00Z"`) {
		t.Fatalf("zero transition time should serialize as the RFC 3339 zero value: %s", raw)
	}
	var backCond Condition
	if err := json.Unmarshal(raw, &backCond); err != nil {
		t.Fatalf("unmarshal condition: %v", err)
	}
	if !backCond.LastTransitionTime.IsZero() {
		t.Fatalf("zero transition time did not round-trip as zero: %v", backCond.LastTransitionTime)
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

func TestApplyDefaultsBlueGreen(t *testing.T) {
	// No blueGreen block at all: the strategy still validates and
	// the gate defaults are filled in, so a bare `strategy:
	// BlueGreen` manifest has a real health gate.
	d := validDeployment()
	d.Spec.Strategy = StrategyBlueGreen
	ApplyDefaults(d)
	if d.Spec.BlueGreen == nil || d.Spec.BlueGreen.Gate == nil {
		t.Fatal("blue-green spec/gate missing after ApplyDefaults")
	}
	if g := d.Spec.BlueGreen.Gate; g.MaxErrorRate != DefaultGateMaxErrorRate || g.MaxP95LatencyMs != DefaultGateMaxP95LatencyMs || g.MinReadyRatio != DefaultGateMinReadyRatio {
		t.Fatalf("default blue-green gate = %+v", g)
	}
	if errs := Validate(d); errs != nil {
		t.Fatalf("defaulted blue-green deployment should validate, got %v", errs)
	}

	// A partial gate keeps the user's value and fills the rest.
	d = validDeployment()
	d.Spec.Strategy = StrategyBlueGreen
	d.Spec.BlueGreen = &BlueGreenSpec{Gate: &GateSpec{MaxErrorRate: 0.2}}
	ApplyDefaults(d)
	if g := d.Spec.BlueGreen.Gate; g.MaxErrorRate != 0.2 || g.MaxP95LatencyMs != DefaultGateMaxP95LatencyMs || g.MinReadyRatio != DefaultGateMinReadyRatio {
		t.Fatalf("partial blue-green gate = %+v", g)
	}

	// A non-blue-green strategy never gains a blue-green spec.
	d = validDeployment()
	ApplyDefaults(d)
	if d.Spec.BlueGreen != nil {
		t.Fatalf("rolling deployment gained a blue-green spec: %+v", d.Spec.BlueGreen)
	}
}
