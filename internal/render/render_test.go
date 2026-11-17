package render

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func boolPtr(v bool) *bool { return &v }

func int64Ptr(v int64) *int64 { return &v }

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
	// The stable selector must require canary=false, or the stable
	// Deployment would adopt canary pods (they share the base
	// labels the Service selects on).
	if got.Selector["ml-operator/canary"] != "false" {
		t.Fatalf("stable selector missing canary=false: %v", got.Selector)
	}
	// Same for green=false: the stable Deployment must never adopt
	// green pods, and the Service selects green=false.
	if got.Selector["ml-operator/green"] != "false" {
		t.Fatalf("stable selector missing green=false: %v", got.Selector)
	}
	if got.Labels["ml-operator/green"] != "false" {
		t.Fatalf("stable pod labels missing green=false: %v", got.Labels)
	}
	if got.Green {
		t.Fatal("stable Deployment must not be marked green")
	}
	if got.Labels["ml-operator/canary"] != "false" {
		t.Fatalf("stable pod labels missing canary=false: %v", got.Labels)
	}
	if got.Labels["ml-operator/version"] != "v2" {
		t.Fatalf("pod labels missing version: %v", got.Labels)
	}
	if got.Labels["ml-operator/managed-by"] != ManagedBy {
		t.Fatalf("managed-by label wrong: %v", got.Labels)
	}
	// Security renders hardened by default: non-root uid 10001,
	// read-only rootfs, no privilege escalation, drop ALL,
	// RuntimeDefault seccomp — on the stable Deployment and,
	// through DeploymentFor, on canary and green ones too.
	if got.PodSecurity != (PodSecurityContext{RunAsNonRoot: true, RunAsUser: 10001, SeccompProfile: "RuntimeDefault"}) {
		t.Fatalf("pod security wrong: %+v", got.PodSecurity)
	}
	if c.Security.ReadOnlyRootFilesystem != true || c.Security.AllowPrivilegeEscalation != false ||
		len(c.Security.DropCapabilities) != 1 || c.Security.DropCapabilities[0] != "ALL" {
		t.Fatalf("container security wrong: %+v", c.Security)
	}
	// An explicit security block renders as written, and the
	// canary Deployment inherits it — a rollout never silently
	// drops the hardening.
	d.Spec.Security = &api.SecuritySpec{
		RunAsNonRoot: boolPtr(false), RunAsUser: int64Ptr(0),
		ReadOnlyRootFilesystem: boolPtr(false), AllowPrivilegeEscalation: boolPtr(true),
		DropCapabilities: []string{"NET_BIND_SERVICE"}, SeccompProfile: "Unconfined",
	}
	api.ApplyDefaults(&d)
	relaxed := DeploymentFor(d)
	if relaxed.PodSecurity != (PodSecurityContext{RunAsNonRoot: false, RunAsUser: 0, SeccompProfile: "Unconfined"}) {
		t.Fatalf("relaxed pod security wrong: %+v", relaxed.PodSecurity)
	}
	if relaxed.Container.Security.ReadOnlyRootFilesystem || !relaxed.Container.Security.AllowPrivilegeEscalation {
		t.Fatalf("relaxed container security wrong: %+v", relaxed.Container.Security)
	}
	canary := CanaryDeploymentFor(d)
	if canary.PodSecurity != relaxed.PodSecurity || len(canary.Container.Security.DropCapabilities) != 1 ||
		canary.Container.Security.DropCapabilities[0] != "NET_BIND_SERVICE" {
		t.Fatalf("canary did not inherit the relaxed security: %+v / %+v", canary.PodSecurity, canary.Container.Security)
	}
}

func TestServiceFor(t *testing.T) {
	d := dep()
	got := ServiceFor(d)
	if got.Name != "iris-mlp" || got.Port != 80 || got.TargetPort != 8080 {
		t.Fatalf("service wrong: %+v", got)
	}
	// The Service selector is the base selector plus green=false —
	// no canary label — so during a canary rollout it matches both
	// stable and canary pods (canary pods carry green=false) and
	// traffic splits by ready-pod count. The canary label lives on
	// the Deployments' selectors, not the Service's. Green pods
	// carry green=true, so the Service never routes to them until
	// the blue-green cutover flips it to GreenServiceFor.
	if got.Selector["app.kubernetes.io/name"] != "iris-mlp" {
		t.Fatalf("service selector wrong: %v", got.Selector)
	}
	if got.Selector["ml-operator/green"] != "false" {
		t.Fatalf("service selector missing green=false: %v", got.Selector)
	}
	if _, ok := got.Selector["ml-operator/canary"]; ok {
		t.Fatal("service selector must not carry the canary label (it selects both pod sets)")
	}

	// After the cutover the Service selects green pods only.
	green := GreenServiceFor(d)
	if green.Selector["ml-operator/green"] != "true" {
		t.Fatalf("green service selector missing green=true: %v", green.Selector)
	}
	if green.Name != got.Name || green.Port != got.Port || green.TargetPort != got.TargetPort {
		t.Fatalf("green service differs from the blue one except the selector: %+v vs %+v", green, got)
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

func TestGreenDeploymentFor(t *testing.T) {
	d := dep() // 3 replicas
	d.Spec.Strategy = api.StrategyBlueGreen
	api.ApplyDefaults(&d)

	got := GreenDeploymentFor(d)
	if got.Name != "iris-mlp-green" || !got.Green {
		t.Fatalf("green identity wrong: %+v", got)
	}
	if got.Canary {
		t.Fatal("green Deployment must not be marked canary")
	}
	// Green soaks at full size: it serves the whole load after the
	// cutover, unlike a canary's weight-sized slice.
	if got.Replicas != 3 {
		t.Fatalf("green replicas = %d, want 3 (full size)", got.Replicas)
	}
	if got.Labels["ml-operator/green"] != "true" {
		t.Fatalf("green label missing: %v", got.Labels)
	}
	if got.Labels["ml-operator/canary"] != "false" {
		t.Fatalf("green pods must carry canary=false: %v", got.Labels)
	}
	// The green selector is disjoint from the stable one: green
	// pods only, so the stable Deployment never adopts them.
	if got.Selector["ml-operator/green"] != "true" || got.Selector["ml-operator/canary"] != "false" {
		t.Fatalf("green selector wrong: %v", got.Selector)
	}
	if got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("green image wrong: %s", got.Container.Image)
	}
}

func TestCanaryDeploymentForWeight(t *testing.T) {
	d := dep() // 3 replicas
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 25, Steps: 4, IntervalSeconds: 30}
	api.ApplyDefaults(&d)

	// An explicit weight overrides the spec weight: 100% of 3 is 3.
	if got := CanaryDeploymentForWeight(d, 100); got.Replicas != 3 {
		t.Fatalf("weight 100: replicas = %d, want 3", got.Replicas)
	}
	// 10% of 3 rounds to 1.
	if got := CanaryDeploymentForWeight(d, 10); got.Replicas != 1 {
		t.Fatalf("weight 10: replicas = %d, want 1", got.Replicas)
	}
	// The explicit-weight render still carries the canary identity.
	got := CanaryDeploymentForWeight(d, 50)
	if !got.Canary || got.Labels["ml-operator/canary"] != "true" || got.Selector["ml-operator/canary"] != "true" {
		t.Fatalf("explicit-weight canary identity wrong: %+v", got)
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

	// Security-only change names each relaxed field, so `mlop
	// diff` tells the author exactly what hardened setting
	// moved — and a security change is never reported in sync.
	live3 := DeploymentFor(dep())
	live3.PodSecurity.RunAsUser = 20000
	live3.PodSecurity.SeccompProfile = "Unconfined"
	live3.Container.Security.ReadOnlyRootFilesystem = false
	live3.Container.Security.DropCapabilities = []string{"NET_BIND_SERVICE"}
	reasons3 := DeploymentDiffReasons(desired, live3)
	if len(reasons3) != 4 {
		t.Fatalf("security-only diff = %v, want 4 reasons (runAsUser, seccompProfile, readOnlyRootFilesystem, dropCapabilities)", reasons3)
	}
	for _, want := range []string{"runAsUser", "seccompProfile", "readOnlyRootFilesystem", "dropCapabilities"} {
		found := false
		for _, r := range reasons3 {
			if strings.Contains(r, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("security diff has no reason mentioning %s: %v", want, reasons3)
		}
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

func TestHPAFor(t *testing.T) {
	// No autoscaling: no HPA object.
	d := dep()
	if _, ok := HPAFor(d); ok {
		t.Fatal("HPAFor without autoscaling must return false")
	}

	// Autoscaling on: HPA carries the spec bounds and targets.
	d.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: 2, MaxReplicas: 8, TargetCPUUtilization: 70, TargetRequestsPerSecond: 100}
	api.ApplyDefaults(&d)
	hpa, ok := HPAFor(d)
	if !ok {
		t.Fatal("HPAFor with autoscaling must return true")
	}
	if hpa.MinReplicas != 2 || hpa.MaxReplicas != 8 || hpa.TargetCPUUtilization != 70 || hpa.TargetRequestsPerSecond != 100 {
		t.Fatalf("HPA = %+v, want min 2 max 8 cpu 70 rps 100", hpa)
	}
	if hpa.Name != "iris-mlp" || hpa.Namespace != "default" {
		t.Fatalf("HPA identity = %s/%s, want default/iris-mlp", hpa.Namespace, hpa.Name)
	}
}

func TestHPADiffReasons(t *testing.T) {
	d := dep()
	d.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: 1, MaxReplicas: 5, TargetCPUUtilization: 70}
	api.ApplyDefaults(&d)
	desired, _ := HPAFor(d)

	if reasons := HPADiffReasons(desired, desired); len(reasons) != 0 {
		t.Fatalf("identical HPAs differ: %v", reasons)
	}
	live := desired
	live.MaxReplicas = 10
	live.TargetCPUUtilization = 80
	if reasons := HPADiffReasons(desired, live); len(reasons) != 2 {
		t.Fatalf("HPA diff = %v, want 2 reasons (maxReplicas, targetCPU)", reasons)
	}
}

// TestPDBFor pins the PDB render: a 3-replica deployment gets a
// PDB with minAvailable 2 (the replicas-1 default) selecting the
// model's base labels, and a single replica gets none — a PDB
// that allows every disruption protects nothing.
func TestPDBFor(t *testing.T) {
	d := dep() // 3 replicas
	pdb, ok := PDBFor(d)
	if !ok {
		t.Fatal("PDBFor with 3 replicas must return true")
	}
	if pdb.MinAvailable != 2 {
		t.Fatalf("minAvailable = %d, want 2 (replicas-1 default)", pdb.MinAvailable)
	}
	if pdb.Name != "iris-mlp" || pdb.Namespace != "default" {
		t.Fatalf("PDB identity = %s/%s, want default/iris-mlp", pdb.Namespace, pdb.Name)
	}
	if pdb.Selector["ml-operator/canary"] != "" || pdb.Selector["ml-operator/green"] != "" {
		t.Fatalf("PDB selector must be the base selector (no canary/green labels): %v", pdb.Selector)
	}
	if pdb.Selector["app.kubernetes.io/name"] != "iris-mlp" {
		t.Fatalf("PDB selector missing app label: %v", pdb.Selector)
	}

	d.Spec.Availability = &api.AvailabilitySpec{MinAvailable: 1, MaxSkew: 2}
	pdb, ok = PDBFor(d)
	if !ok || pdb.MinAvailable != 1 {
		t.Fatalf("explicit minAvailable 1 rendered as %+v (ok=%v), want 1", pdb, ok)
	}

	single := dep()
	one := int32(1)
	single.Spec.Replicas = &one
	api.ApplyDefaults(&single)
	if _, ok := PDBFor(single); ok {
		t.Fatal("PDBFor with 1 replica must return false")
	}
}

// TestPDBDiffReasons pins the PDB diff: identical PDBs are a
// no-op, a minAvailable change and a selector change each produce
// one reason.
func TestPDBDiffReasons(t *testing.T) {
	d := dep()
	desired, _ := PDBFor(d)
	if reasons := PDBDiffReasons(desired, desired); len(reasons) != 0 {
		t.Fatalf("identical PDBs differ: %v", reasons)
	}
	live := desired
	live.MinAvailable = 1
	if reasons := PDBDiffReasons(desired, live); len(reasons) != 1 {
		t.Fatalf("PDB diff = %v, want 1 reason (minAvailable)", reasons)
	}
	live = desired
	live.Selector = map[string]string{"app.kubernetes.io/name": "other"}
	if reasons := PDBDiffReasons(desired, live); len(reasons) != 1 {
		t.Fatalf("PDB diff = %v, want 1 reason (selector)", reasons)
	}
}

// TestDeploymentHardening pins the pod-template hardening: a
// multi-replica Deployment renders anti-affinity on and one
// topology-spread constraint (maxSkew 1, hostname, ScheduleAnyway),
// a single replica renders neither, and an explicit opt-out
// removes both.
func TestDeploymentHardening(t *testing.T) {
	d := dep() // 3 replicas, defaults
	got := DeploymentFor(d)
	if !got.AntiAffinity {
		t.Fatal("default Deployment must render anti-affinity on")
	}
	if len(got.TopologySpread) != 1 {
		t.Fatalf("topology spread = %v, want 1 constraint", got.TopologySpread)
	}
	c := got.TopologySpread[0]
	if c.MaxSkew != 1 || c.TopologyKey != "kubernetes.io/hostname" || c.WhenUnsatisfiable != "ScheduleAnyway" {
		t.Fatalf("spread constraint wrong: %+v", c)
	}
	if c.LabelSelector["app.kubernetes.io/name"] != "iris-mlp" {
		t.Fatalf("spread selector wrong: %v", c.LabelSelector)
	}

	one := int32(1)
	single := dep()
	single.Spec.Replicas = &one
	api.ApplyDefaults(&single)
	got = DeploymentFor(single)
	if len(got.TopologySpread) != 0 {
		t.Fatalf("single-replica Deployment must render no spread constraint, got %v", got.TopologySpread)
	}
	if !got.AntiAffinity {
		t.Fatal("single-replica Deployment keeps anti-affinity (harmless, one pod has nothing to avoid)")
	}

	off := dep()
	off.Spec.Availability = &api.AvailabilitySpec{AntiAffinity: boolPtr(false), MaxSkew: 2}
	api.ApplyDefaults(&off)
	got = DeploymentFor(off)
	if got.AntiAffinity {
		t.Fatal("explicit antiAffinity false must render off")
	}
	if got.TopologySpread[0].MaxSkew != 2 {
		t.Fatalf("explicit maxSkew 2 rendered as %d", got.TopologySpread[0].MaxSkew)
	}
}

// TestDeploymentPlacement pins the render: a placement block
// lands on the pod template of the stable Deployment and is
// inherited by canary and green Deployments, so a rollout never
// silently moves the model off its nodes. An unconstrained
// deployment renders no selector and no tolerations.
func TestDeploymentPlacement(t *testing.T) {
	d := dep()
	got := DeploymentFor(d)
	if got.NodeSelector != nil || got.Tolerations != nil {
		t.Fatalf("unconstrained Deployment must render no placement, got %v / %v", got.NodeSelector, got.Tolerations)
	}

	secs := int64(300)
	d.Spec.Placement = &api.PlacementSpec{
		NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"},
		Tolerations: []api.Toleration{
			{Key: "gpu", Value: "true", Effect: "NoSchedule"},
			{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &secs},
		},
	}
	got = DeploymentFor(d)
	if got.NodeSelector["accelerator"] != "nvidia-tesla-t4" {
		t.Fatalf("nodeSelector wrong: %v", got.NodeSelector)
	}
	if len(got.Tolerations) != 2 || got.Tolerations[0].Key != "gpu" || got.Tolerations[0].Value != "true" ||
		got.Tolerations[1].Operator != "Exists" || *got.Tolerations[1].TolerationSeconds != 300 {
		t.Fatalf("tolerations wrong: %+v", got.Tolerations)
	}
	// Placement is a property of the model, not of one revision:
	// canary and green Deployments inherit it, so a rollout
	// never silently moves the model off its nodes.
	canary := CanaryDeploymentFor(d)
	if canary.NodeSelector["accelerator"] != "nvidia-tesla-t4" || len(canary.Tolerations) != 2 {
		t.Fatalf("canary did not inherit the placement: %v / %+v", canary.NodeSelector, canary.Tolerations)
	}
	green := GreenDeploymentFor(d)
	if green.NodeSelector["accelerator"] != "nvidia-tesla-t4" || len(green.Tolerations) != 2 {
		t.Fatalf("green did not inherit the placement: %v / %+v", green.NodeSelector, green.Tolerations)
	}
	// The render copies the placement: mutating the spec after
	// rendering must not move a Deployment that was already
	// rendered.
	d.Spec.Placement.NodeSelector["accelerator"] = "other"
	*d.Spec.Placement.Tolerations[1].TolerationSeconds = 60
	if got.NodeSelector["accelerator"] != "nvidia-tesla-t4" || *got.Tolerations[1].TolerationSeconds != 300 {
		t.Fatalf("render aliased the spec placement: %v / %+v", got.NodeSelector, got.Tolerations)
	}
}

// TestDeploymentDiffReasonsPlacement pins the diff: a
// nodeSelector change and a tolerations change each surface as a
// reason, so the reconciler updates the Deployment instead of
// reporting a no-op — and a JSON round-trip of the seconds does
// not produce a phantom diff.
func TestDeploymentDiffReasonsPlacement(t *testing.T) {
	d := dep()
	d.Spec.Placement = &api.PlacementSpec{
		NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"},
		Tolerations:  []api.Toleration{{Key: "gpu", Value: "true", Effect: "NoSchedule"}},
	}
	desired := DeploymentFor(d)
	if reasons := DeploymentDiffReasons(desired, desired); len(reasons) != 0 {
		t.Fatalf("identical Deployments differ: %v", reasons)
	}
	live := desired
	live.NodeSelector = map[string]string{"accelerator": "nvidia-tesla-v100"}
	if reasons := DeploymentDiffReasons(desired, live); len(reasons) != 1 || reasons[0] != "nodeSelector changed" {
		t.Fatalf("Deployment diff = %v, want [nodeSelector changed]", reasons)
	}
	live = desired
	live.Tolerations = []api.Toleration{{Key: "gpu", Value: "true", Effect: "PreferNoSchedule"}}
	if reasons := DeploymentDiffReasons(desired, live); len(reasons) != 1 || reasons[0] != "tolerations changed" {
		t.Fatalf("Deployment diff = %v, want [tolerations changed]", reasons)
	}
	// Seconds compare by value, not by pointer: a spec that
	// survived a JSON round-trip (state file) must still equal
	// the freshly rendered Deployment.
	secs := int64(300)
	d.Spec.Placement.Tolerations = []api.Toleration{
		{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &secs},
	}
	desired = DeploymentFor(d)
	live = desired
	other := int64(300)
	live.Tolerations = []api.Toleration{
		{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &other},
	}
	if reasons := DeploymentDiffReasons(desired, live); len(reasons) != 0 {
		t.Fatalf("same toleration seconds via different pointers differ: %v", reasons)
	}
}
func TestDeploymentDiffReasonsHardening(t *testing.T) {
	d := dep()
	desired := DeploymentFor(d)
	if reasons := DeploymentDiffReasons(desired, desired); len(reasons) != 0 {
		t.Fatalf("identical Deployments differ: %v", reasons)
	}
	live := desired
	live.AntiAffinity = false
	if reasons := DeploymentDiffReasons(desired, live); len(reasons) != 1 {
		t.Fatalf("Deployment diff = %v, want 1 reason (antiAffinity)", reasons)
	}
	live = desired
	// Copy the slice: a struct copy shares the backing array, so
	// mutating live.TopologySpread[0] would mutate desired too.
	live.TopologySpread = append([]TopologySpreadConstraint(nil), desired.TopologySpread...)
	live.TopologySpread[0].MaxSkew = 2
	if reasons := DeploymentDiffReasons(desired, live); len(reasons) != 1 {
		t.Fatalf("Deployment diff = %v, want 1 reason (topology spread)", reasons)
	}
}
