package api

// Phase is the lifecycle phase of a ModelDeployment, reported in status.
type Phase string

const (
	// PhasePending is the initial phase: the deployment is registered
	// but the operator has not started provisioning it yet.
	PhasePending Phase = "Pending"
	// PhaseProgressing means the operator is provisioning or rolling
	// out the serving Deployment.
	PhaseProgressing Phase = "Progressing"
	// PhaseAvailable means the desired replicas are ready and serving.
	PhaseAvailable Phase = "Available"
	// PhaseDegraded means the model is serving but below the desired
	// replica count, or a rollout is stuck behind a health gate.
	PhaseDegraded Phase = "Degraded"
	// PhaseFailed means the deployment could not be provisioned
	// (invalid spec that passed admission, image pull failure, ...).
	PhaseFailed Phase = "Failed"
)

// RolloutStrategy selects how a new model revision replaces the old one.
type RolloutStrategy string

const (
	// StrategyRecreate tears the old pods down before starting new ones.
	StrategyRecreate RolloutStrategy = "Recreate"
	// StrategyRolling replaces pods gradually (maxUnavailable 25%).
	StrategyRolling RolloutStrategy = "RollingUpdate"
	// StrategyCanary shifts a percentage of traffic to the new revision
	// and holds until the health gate passes.
	StrategyCanary RolloutStrategy = "Canary"
	// StrategyBlueGreen runs the new revision at full size beside
	// the old one, then flips the Service to it once the health
	// gate passes. Traffic never splits: clients see either the
	// old version or the new one, never both at once.
	StrategyBlueGreen RolloutStrategy = "BlueGreen"
)

// ModelDeployment is the custom resource the operator watches. It is
// deliberately plain Go (no client-go types) so the API package stays
// dependency-free and usable from the CLI and tests.
type ModelDeployment struct {
	// Name is the deployment name; must be a DNS-1123 subdomain.
	Name string
	// Namespace scopes the deployment; empty means "default".
	Namespace string
	// Generation is bumped by the API server on every spec change; the
	// operator records the last generation it acted on in status.
	Generation int64

	Spec   ModelDeploymentSpec
	Status ModelDeploymentStatus
}

// ModelDeploymentSpec is the desired state of a deployed model.
type ModelDeploymentSpec struct {
	// Model is the model artifact to serve.
	Model ModelSpec
	// Replicas is the desired pod count; nil means the default (1).
	// Autoscaling, when enabled, overrides this at runtime.
	Replicas *int32
	// Resources are the per-pod CPU/memory requests and limits.
	Resources ResourceRequirements
	// Port is the container port the model server listens on.
	Port int32
	// Strategy selects the rollout behaviour for new revisions.
	Strategy RolloutStrategy
	// Canary configures a canary rollout; required when Strategy is
	// Canary, ignored otherwise.
	Canary *CanarySpec
	// BlueGreen configures a blue-green rollout; optional when
	// Strategy is BlueGreen (a nil config takes the operator
	// defaults), ignored otherwise.
	BlueGreen *BlueGreenSpec
	// Autoscaling enables the HorizontalPodAutoscaler for this model.
	Autoscaling *AutoscalingSpec
	// Env holds extra environment variables for the model container.
	Env map[string]string
	// Health configures the readiness gate for the model container.
	Health HealthSpec
}

// ModelSpec identifies the model artifact and serving runtime.
type ModelSpec struct {
	// Image is the container image serving the model
	// (e.g. "registry.example.com/iris-mlp:v2").
	Image string
	// Framework names the serving runtime (e.g. "onnx", "torchserve",
	// "triton", "custom"). Free-form; the operator only uses it for
	// the default readiness path when Health.Path is empty.
	Framework string
	// Version is the model version label recorded in status and metrics.
	Version string
}

// ResourceRequirements mirrors the Kubernetes requests/limits pair,
// with quantities as strings ("500m", "1Gi") so the API package has
// no resource-quantity dependency.
type ResourceRequirements struct {
	// CPURequest is the CPU request, e.g. "500m". Empty means unset.
	CPURequest string
	// CPULimit is the CPU limit, e.g. "2". Empty means unset.
	CPULimit string
	// MemoryRequest is the memory request, e.g. "512Mi".
	MemoryRequest string
	// MemoryLimit is the memory limit, e.g. "1Gi".
	MemoryLimit string
}

// CanarySpec configures a canary rollout.
type CanarySpec struct {
	// Weight is the initial percentage of traffic sent to the new
	// revision (1-99). The operator steps it up after each health
	// gate passes.
	Weight int32
	// Steps is the number of traffic increments to reach 100%.
	// 0 means a single step (Weight straight to 100% after the gate).
	Steps int32
	// IntervalSeconds is the pause between steps.
	IntervalSeconds int32
	// Gate overrides the health-gate thresholds for this rollout.
	// Nil means the operator defaults (5% errors, 500ms p95, all
	// canary pods ready). A rollout can loosen or tighten the gate
	// — a latency-sensitive model gets a lower p95 cap, a noisy
	// one a higher error budget — but a threshold never disables
	// its check: a zero field takes the default, it does not turn
	// the check off.
	Gate *GateSpec
}

// BlueGreenSpec configures a blue-green rollout.
type BlueGreenSpec struct {
	// Gate overrides the health-gate thresholds for this rollout.
	// Nil means the operator defaults (5% errors, 500ms p95, all
	// green pods ready). Like a canary gate, a threshold never
	// disables its check: a zero field takes the default, it does
	// not turn the check off.
	Gate *GateSpec
}

// GateSpec sets the health-gate thresholds for one canary rollout.
// It mirrors rollout.Gate, minus the "0 disables the check"
// semantics: here a zero field means "use the operator default",
// because ApplyDefaults fills every zero field before the gate is
// ever evaluated.
type GateSpec struct {
	// MaxErrorRate is the maximum tolerated error fraction (0-1).
	// 0 means the default (5%).
	MaxErrorRate float64
	// MaxP95LatencyMs is the maximum tolerated p95 latency in
	// milliseconds. 0 means the default (500ms).
	MaxP95LatencyMs float64
	// MinReadyRatio is the minimum ready/desired canary replicas
	// (0-1). 0 means the default (1.0, every canary pod ready).
	MinReadyRatio float64
}

// AutoscalingSpec configures horizontal autoscaling for the model.
type AutoscalingSpec struct {
	// MinReplicas is the floor; 0 means unset (defaults to 1).
	MinReplicas int32
	// MaxReplicas is the ceiling; must be >= MinReplicas.
	MaxReplicas int32
	// TargetCPUUtilization is the average CPU percentage that triggers
	// scaling (1-100). 0 disables CPU-based scaling.
	TargetCPUUtilization int32
	// TargetRequestsPerSecond is the per-pod request rate that triggers
	// scaling. 0 disables RPS-based scaling.
	TargetRequestsPerSecond int32
}

// HealthSpec configures the readiness gate for the model container.
type HealthSpec struct {
	// Path is the HTTP readiness endpoint, e.g. "/healthz".
	Path string
	// InitialDelaySeconds is the wait after container start before the
	// first probe.
	InitialDelaySeconds int32
	// PeriodSeconds is the probe interval.
	PeriodSeconds int32
}

// ModelDeploymentStatus is the observed state of a deployed model.
type ModelDeploymentStatus struct {
	// Phase is the current lifecycle phase.
	Phase Phase
	// ReadyReplicas is the number of pods passing the readiness gate.
	ReadyReplicas int32
	// ObservedGeneration is the last spec generation the operator acted on.
	ObservedGeneration int64
	// Message is a human-readable summary of the current state.
	Message string
	// PreviousImage is the container image the stable Deployment
	// served before the current one (e.g. the v1 image after a
	// v2 promotion). Empty until the first image change. `mlop
	// rollback` restores it. The reconciler updates it whenever
	// the stable image actually changes — on a canary promotion
	// and on a direct (rolling/recreate) image change — and
	// preserves it across reconciles that change nothing.
	PreviousImage string
	// PreviousVersion is the model version label that went with
	// PreviousImage.
	PreviousVersion string
	// Conditions are the latest condition per type (Ready, Progressing,
	// Available), newest first within a type is not guaranteed — use
	// ConditionFor to look one up.
	Conditions []Condition
}

// Condition is a single status condition, following the Kubernetes
// condition conventions (Type/Status/Reason/Message).
type Condition struct {
	Type    string
	Status  string // "True", "False", "Unknown"
	Reason  string
	Message string
}

// ConditionFor returns the condition of the given type, or nil.
func (s *ModelDeploymentStatus) ConditionFor(condType string) *Condition {
	for i := range s.Conditions {
		if s.Conditions[i].Type == condType {
			return &s.Conditions[i]
		}
	}
	return nil
}

// SetCondition upserts a condition of the given type.
func (s *ModelDeploymentStatus) SetCondition(c Condition) {
	for i := range s.Conditions {
		if s.Conditions[i].Type == c.Type {
			s.Conditions[i] = c
			return
		}
	}
	s.Conditions = append(s.Conditions, c)
}

// Key returns the registry key for this deployment ("namespace/name").
func (d *ModelDeployment) Key() string {
	ns := d.Namespace
	if ns == "" {
		ns = "default"
	}
	return ns + "/" + d.Name
}
