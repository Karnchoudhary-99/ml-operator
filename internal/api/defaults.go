package api

// Default values applied by ApplyDefaults. These are the operator's
// opinions for a minimal manifest: one replica, the standard serving
// port, a rolling rollout, and modest resource requests/limits.
const (
	DefaultReplicas           int32 = 1
	DefaultPort               int32 = 8080
	DefaultStrategy                 = StrategyRolling
	DefaultCPURequest               = "500m"
	DefaultCPULimit                 = "1"
	DefaultMemoryRequest            = "512Mi"
	DefaultMemoryLimit              = "1Gi"
	DefaultHealthPath               = "/healthz"
	DefaultHealthInitialDelay int32 = 5
	DefaultHealthPeriod       int32 = 10
	DefaultCanaryInterval     int32 = 30
	DefaultAutoscalingMin     int32 = 1
	// DefaultGateMaxErrorRate is the canary health gate's error
	// budget when a rollout sets no gate thresholds of its own.
	DefaultGateMaxErrorRate float64 = 0.05
	// DefaultGateMaxP95LatencyMs is the canary health gate's p95
	// latency cap when a rollout sets no gate thresholds.
	DefaultGateMaxP95LatencyMs float64 = 500
	// DefaultGateMinReadyRatio is the canary health gate's ready
	// ratio when a rollout sets no gate thresholds: every canary
	// pod must be ready before the rollout advances.
	DefaultGateMinReadyRatio float64 = 1.0
)

// ApplyDefaults fills in every unset field of the deployment's spec
// with the operator defaults. It never overwrites a value the user
// set. Call it before Validate when admitting a manifest.
func ApplyDefaults(d *ModelDeployment) {
	if d.Namespace == "" {
		d.Namespace = "default"
	}
	spec := &d.Spec
	if spec.Replicas == nil {
		r := DefaultReplicas
		spec.Replicas = &r
	}
	if spec.Port == 0 {
		spec.Port = DefaultPort
	}
	if spec.Strategy == "" {
		spec.Strategy = DefaultStrategy
	}
	if spec.Resources.CPURequest == "" {
		spec.Resources.CPURequest = DefaultCPURequest
	}
	if spec.Resources.CPULimit == "" {
		spec.Resources.CPULimit = DefaultCPULimit
	}
	if spec.Resources.MemoryRequest == "" {
		spec.Resources.MemoryRequest = DefaultMemoryRequest
	}
	if spec.Resources.MemoryLimit == "" {
		spec.Resources.MemoryLimit = DefaultMemoryLimit
	}
	if spec.Strategy == StrategyCanary && spec.Canary != nil {
		if spec.Canary.IntervalSeconds == 0 {
			spec.Canary.IntervalSeconds = DefaultCanaryInterval
		}
	}
	if spec.Autoscaling != nil && spec.Autoscaling.MinReplicas == 0 {
		spec.Autoscaling.MinReplicas = DefaultAutoscalingMin
	}
	if spec.Health.Path == "" {
		// Framework-specific defaults: Triton serves readiness on
		// /v2/health/ready, everything else on /healthz.
		if spec.Model.Framework == "triton" {
			spec.Health.Path = "/v2/health/ready"
		} else {
			spec.Health.Path = DefaultHealthPath
		}
	}
	if spec.Health.InitialDelaySeconds == 0 {
		spec.Health.InitialDelaySeconds = DefaultHealthInitialDelay
	}
	if spec.Health.PeriodSeconds == 0 {
		spec.Health.PeriodSeconds = DefaultHealthPeriod
	}
}
