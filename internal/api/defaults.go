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
	// DefaultRunAsUser is the non-root uid every model container
	// runs as when the manifest sets no security block.
	DefaultRunAsUser int64 = 10001
	// DefaultSeccompProfile is the seccomp profile applied to
	// every model pod when the manifest sets no security block.
	DefaultSeccompProfile = "RuntimeDefault"
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
		// Gate thresholds: a missing gate gets the full default
		// set; a partial gate keeps the user's values and fills
		// only the zero fields, so a rollout that sets just an
		// error budget still gets the default latency cap and
		// ready ratio instead of silently disabling those checks.
		spec.Canary.Gate = defaultGate(spec.Canary.Gate)
	}
	if spec.Strategy == StrategyBlueGreen {
		// Same gate-defaulting as a canary: a missing blue-green
		// spec (or a missing/partial gate inside it) takes the
		// operator defaults, so a BlueGreen strategy with no
		// blueGreen block at all still has a real health gate.
		if spec.BlueGreen == nil {
			spec.BlueGreen = &BlueGreenSpec{}
		}
		spec.BlueGreen.Gate = defaultGate(spec.BlueGreen.Gate)
	}
	if spec.Autoscaling != nil && spec.Autoscaling.MinReplicas == 0 {
		spec.Autoscaling.MinReplicas = DefaultAutoscalingMin
	}
	// Availability defaults: anti-affinity on, maxSkew 1,
	// minAvailable left at 0 so it resolves to replicas-1 at
	// render time (see AvailabilitySpec.EffectiveMinAvailable).
	// A nil spec takes the full default set; a partial spec keeps
	// the user's values and fills only the unset anti-affinity
	// flag, so `availability: {minAvailable: 1}` does not
	// silently turn anti-affinity off.
	if spec.Availability == nil {
		spec.Availability = &AvailabilitySpec{AntiAffinity: boolPtr(true)}
	} else if spec.Availability.AntiAffinity == nil {
		spec.Availability.AntiAffinity = boolPtr(true)
	}
	// Security defaults: non-root uid 10001, read-only root
	// filesystem, no privilege escalation, all capabilities
	// dropped, RuntimeDefault seccomp. A nil spec takes the full
	// default set; a partial spec keeps the user's values and
	// fills only the unset fields, so `security: {runAsUser:
	// 20000}` does not silently turn the read-only rootfs off.
	if spec.Security == nil {
		spec.Security = &SecuritySpec{
			RunAsNonRoot:             boolPtr(true),
			RunAsUser:                int64Ptr(DefaultRunAsUser),
			ReadOnlyRootFilesystem:   boolPtr(true),
			AllowPrivilegeEscalation: boolPtr(false),
			DropCapabilities:         []string{"ALL"},
			SeccompProfile:           DefaultSeccompProfile,
		}
	} else {
		if spec.Security.RunAsNonRoot == nil {
			spec.Security.RunAsNonRoot = boolPtr(true)
		}
		if spec.Security.RunAsUser == nil {
			spec.Security.RunAsUser = int64Ptr(DefaultRunAsUser)
		}
		if spec.Security.ReadOnlyRootFilesystem == nil {
			spec.Security.ReadOnlyRootFilesystem = boolPtr(true)
		}
		if spec.Security.AllowPrivilegeEscalation == nil {
			spec.Security.AllowPrivilegeEscalation = boolPtr(false)
		}
		if len(spec.Security.DropCapabilities) == 0 {
			spec.Security.DropCapabilities = []string{"ALL"}
		}
		if spec.Security.SeccompProfile == "" {
			spec.Security.SeccompProfile = DefaultSeccompProfile
		}
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

// boolPtr returns a pointer to v, for defaulting optional booleans
// (AvailabilitySpec.AntiAffinity) without a named variable.
func boolPtr(v bool) *bool { return &v }

// int64Ptr returns a pointer to v, for defaulting optional int64s
// (SecuritySpec.RunAsUser) without a named variable.
func int64Ptr(v int64) *int64 { return &v }

// defaultGate returns g with every zero threshold filled from the
// operator defaults, or a fresh default gate when g is nil. A
// zero field means "use the default", never "disable the check".
func defaultGate(g *GateSpec) *GateSpec {
	if g == nil {
		return &GateSpec{
			MaxErrorRate:    DefaultGateMaxErrorRate,
			MaxP95LatencyMs: DefaultGateMaxP95LatencyMs,
			MinReadyRatio:   DefaultGateMinReadyRatio,
		}
	}
	if g.MaxErrorRate == 0 {
		g.MaxErrorRate = DefaultGateMaxErrorRate
	}
	if g.MaxP95LatencyMs == 0 {
		g.MaxP95LatencyMs = DefaultGateMaxP95LatencyMs
	}
	if g.MinReadyRatio == 0 {
		g.MinReadyRatio = DefaultGateMinReadyRatio
	}
	return g
}
