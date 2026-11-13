package api

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Validate returns every problem with the deployment's spec, or nil
// when the spec is valid. Validation is deliberately strict: a bad
// spec must fail at admission time, never halfway through a rollout.
func Validate(d *ModelDeployment) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if !isDNS1123Subdomain(d.Name) {
		add("name %q must be a DNS-1123 subdomain (lowercase letters, digits, '-', '.')", d.Name)
	}
	if d.Namespace != "" && !isDNS1123Subdomain(d.Namespace) {
		add("namespace %q must be a DNS-1123 subdomain", d.Namespace)
	}

	spec := d.Spec
	if strings.TrimSpace(spec.Model.Image) == "" {
		add("spec.model.image must not be empty")
	}
	if spec.Replicas != nil && *spec.Replicas < 0 {
		add("spec.replicas must be >= 0, got %d", *spec.Replicas)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		add("spec.port must be between 1 and 65535, got %d", spec.Port)
	}

	errs = append(errs, validateResources(spec.Resources)...)
	errs = append(errs, validateStrategy(spec)...)
	errs = append(errs, validateAutoscaling(spec.Autoscaling)...)
	errs = append(errs, validateEnv(spec.Env)...)
	errs = append(errs, validateHealth(spec.Health)...)

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateResources(r ResourceRequirements) []error {
	var errs []error
	check := func(field, value string, cpu bool) {
		if value == "" {
			return
		}
		if _, err := ParseQuantity(value, cpu); err != nil {
			errs = append(errs, fmt.Errorf("spec.resources.%s: %w", field, err))
		}
	}
	check("cpuRequest", r.CPURequest, true)
	check("cpuLimit", r.CPULimit, true)
	check("memoryRequest", r.MemoryRequest, false)
	check("memoryLimit", r.MemoryLimit, false)

	// A limit below its request is a spec error, not a runtime surprise.
	if r.CPURequest != "" && r.CPULimit != "" {
		req, errReq := ParseQuantity(r.CPURequest, true)
		lim, errLim := ParseQuantity(r.CPULimit, true)
		if errReq == nil && errLim == nil && lim < req {
			errs = append(errs, fmt.Errorf("spec.resources.cpuLimit (%s) must be >= cpuRequest (%s)", r.CPULimit, r.CPURequest))
		}
	}
	if r.MemoryRequest != "" && r.MemoryLimit != "" {
		req, errReq := ParseQuantity(r.MemoryRequest, false)
		lim, errLim := ParseQuantity(r.MemoryLimit, false)
		if errReq == nil && errLim == nil && lim < req {
			errs = append(errs, fmt.Errorf("spec.resources.memoryLimit (%s) must be >= memoryRequest (%s)", r.MemoryLimit, r.MemoryRequest))
		}
	}
	return errs
}

func validateStrategy(spec ModelDeploymentSpec) []error {
	var errs []error
	switch spec.Strategy {
	case "", StrategyRecreate, StrategyRolling, StrategyCanary:
		// "" is accepted here; ApplyDefaults fills it in.
	default:
		errs = append(errs, fmt.Errorf("spec.strategy %q must be one of Recreate, RollingUpdate, Canary", spec.Strategy))
		return errs
	}
	if spec.Strategy == StrategyCanary {
		if spec.Canary == nil {
			errs = append(errs, fmt.Errorf("spec.canary is required when strategy is Canary"))
			return errs
		}
		if spec.Canary.Weight < 1 || spec.Canary.Weight > 99 {
			errs = append(errs, fmt.Errorf("spec.canary.weight must be between 1 and 99, got %d", spec.Canary.Weight))
		}
		if spec.Canary.Steps < 0 {
			errs = append(errs, fmt.Errorf("spec.canary.steps must be >= 0, got %d", spec.Canary.Steps))
		}
		if spec.Canary.IntervalSeconds < 0 {
			errs = append(errs, fmt.Errorf("spec.canary.intervalSeconds must be >= 0, got %d", spec.Canary.IntervalSeconds))
		}
		if g := spec.Canary.Gate; g != nil {
			if g.MaxErrorRate < 0 || g.MaxErrorRate > 1 {
				errs = append(errs, fmt.Errorf("spec.canary.gate.maxErrorRate must be between 0 and 1, got %g", g.MaxErrorRate))
			}
			if g.MaxP95LatencyMs < 0 {
				errs = append(errs, fmt.Errorf("spec.canary.gate.maxP95LatencyMs must be >= 0, got %g", g.MaxP95LatencyMs))
			}
			if g.MinReadyRatio < 0 || g.MinReadyRatio > 1 {
				errs = append(errs, fmt.Errorf("spec.canary.gate.minReadyRatio must be between 0 and 1, got %g", g.MinReadyRatio))
			}
		}
	}
	return errs
}

func validateAutoscaling(a *AutoscalingSpec) []error {
	if a == nil {
		return nil
	}
	var errs []error
	if a.MinReplicas < 0 {
		errs = append(errs, fmt.Errorf("spec.autoscaling.minReplicas must be >= 0, got %d", a.MinReplicas))
	}
	if a.MaxReplicas < 1 {
		errs = append(errs, fmt.Errorf("spec.autoscaling.maxReplicas must be >= 1, got %d", a.MaxReplicas))
	}
	if a.MaxReplicas >= 1 && a.MinReplicas > a.MaxReplicas {
		errs = append(errs, fmt.Errorf("spec.autoscaling.minReplicas (%d) must be <= maxReplicas (%d)", a.MinReplicas, a.MaxReplicas))
	}
	if a.TargetCPUUtilization < 0 || a.TargetCPUUtilization > 100 {
		errs = append(errs, fmt.Errorf("spec.autoscaling.targetCPUUtilization must be between 0 and 100, got %d", a.TargetCPUUtilization))
	}
	if a.TargetRequestsPerSecond < 0 {
		errs = append(errs, fmt.Errorf("spec.autoscaling.targetRequestsPerSecond must be >= 0, got %d", a.TargetRequestsPerSecond))
	}
	if a.TargetCPUUtilization == 0 && a.TargetRequestsPerSecond == 0 {
		errs = append(errs, fmt.Errorf("spec.autoscaling needs at least one target (targetCPUUtilization or targetRequestsPerSecond)"))
	}
	return errs
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateEnv(env map[string]string) []error {
	var errs []error
	for k := range env {
		if !envKeyRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("spec.env key %q must match [A-Za-z_][A-Za-z0-9_]*", k))
		}
	}
	return errs
}

func validateHealth(h HealthSpec) []error {
	var errs []error
	if h.Path != "" && !strings.HasPrefix(h.Path, "/") {
		errs = append(errs, fmt.Errorf("spec.health.path %q must start with '/'", h.Path))
	}
	if h.InitialDelaySeconds < 0 {
		errs = append(errs, fmt.Errorf("spec.health.initialDelaySeconds must be >= 0, got %d", h.InitialDelaySeconds))
	}
	if h.PeriodSeconds < 0 {
		errs = append(errs, fmt.Errorf("spec.health.periodSeconds must be >= 0, got %d", h.PeriodSeconds))
	}
	return errs
}

// ParseQuantity parses a Kubernetes-style resource quantity into a
// comparable integer: millicores for CPU ("500m" -> 500, "2" -> 2000),
// bytes for memory ("512Mi" -> 536870912, "1G" -> 1000000000).
// It returns an error for malformed quantities.
func ParseQuantity(q string, cpu bool) (int64, error) {
	if q == "" {
		return 0, fmt.Errorf("quantity must not be empty")
	}
	if cpu {
		return parseCPU(q)
	}
	return parseMemory(q)
}

var quantityRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([a-zA-Z]*)$`)

func parseCPU(q string) (int64, error) {
	m := quantityRe.FindStringSubmatch(q)
	if m == nil {
		return 0, fmt.Errorf("invalid CPU quantity %q", q)
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CPU quantity %q", q)
	}
	switch m[2] {
	case "m":
		return int64(value), nil
	case "":
		return int64(value * 1000), nil
	default:
		return 0, fmt.Errorf("invalid CPU quantity %q (unknown suffix %q)", q, m[2])
	}
}

var memorySuffixes = map[string]int64{
	"": 1, "Ki": 1024, "Mi": 1024 * 1024, "Gi": 1024 * 1024 * 1024,
	"Ti": 1024 * 1024 * 1024 * 1024,
	"k":  1000, "M": 1000 * 1000, "G": 1000 * 1000 * 1000,
	"T": 1000 * 1000 * 1000 * 1000,
}

func parseMemory(q string) (int64, error) {
	m := quantityRe.FindStringSubmatch(q)
	if m == nil {
		return 0, fmt.Errorf("invalid memory quantity %q", q)
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory quantity %q", q)
	}
	mult, ok := memorySuffixes[m[2]]
	if !ok {
		return 0, fmt.Errorf("invalid memory quantity %q (unknown suffix %q)", q, m[2])
	}
	return int64(value * float64(mult)), nil
}

var dns1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func isDNS1123Subdomain(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !dns1123Re.MatchString(s) {
		return false
	}
	// No empty labels ("a..b") and no label starting/ending with '-'.
	for _, label := range strings.Split(s, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}
