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
	errs = append(errs, validateAvailability(spec)...)
	errs = append(errs, validateMaintenanceWindow(spec.MaintenanceWindow)...)
	errs = append(errs, validateSecurity(spec)...)
	errs = append(errs, validatePlacement(spec)...)
	errs = append(errs, validateModelSource(spec)...)
	errs = append(errs, validateImagePullSecrets(spec)...)
	errs = append(errs, validateMetadata(spec)...)
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
	case "", StrategyRecreate, StrategyRolling, StrategyCanary, StrategyBlueGreen:
		// "" is accepted here; ApplyDefaults fills it in.
	default:
		errs = append(errs, fmt.Errorf("spec.strategy %q must be one of Recreate, RollingUpdate, Canary, BlueGreen", spec.Strategy))
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
		errs = append(errs, validateGate(spec.Canary.Gate, "spec.canary.gate")...)
	}
	if spec.Strategy == StrategyBlueGreen {
		// A blue-green rollout needs no config of its own: the
		// green Deployment is always full-size, so a nil spec
		// just takes the operator gate defaults. A gate that is
		// present still has to be in range.
		errs = append(errs, validateGate(gateOf(spec.BlueGreen), "spec.blueGreen.gate")...)
	}
	return errs
}

// gateOf returns the gate of a blue-green spec, or nil when the spec
// (or its gate) is absent.
func gateOf(b *BlueGreenSpec) *GateSpec {
	if b == nil {
		return nil
	}
	return b.Gate
}

// validateGate checks one gate's thresholds. A nil gate is valid —
// ApplyDefaults fills it in before the gate is ever evaluated.
func validateGate(g *GateSpec, path string) []error {
	if g == nil {
		return nil
	}
	var errs []error
	if g.MaxErrorRate < 0 || g.MaxErrorRate > 1 {
		errs = append(errs, fmt.Errorf("%s.maxErrorRate must be between 0 and 1, got %g", path, g.MaxErrorRate))
	}
	if g.MaxP95LatencyMs < 0 {
		errs = append(errs, fmt.Errorf("%s.maxP95LatencyMs must be >= 0, got %g", path, g.MaxP95LatencyMs))
	}
	if g.MinReadyRatio < 0 || g.MinReadyRatio > 1 {
		errs = append(errs, fmt.Errorf("%s.minReadyRatio must be between 0 and 1, got %g", path, g.MinReadyRatio))
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

// validateAvailability checks the availability block. A nil block
// is valid — ApplyDefaults fills it in before the spec is ever
// rendered. minAvailable must stay below the replica count: a PDB
// that demands every replica available blocks every voluntary
// eviction, including the node drain the PDB exists to survive.
func validateMaintenanceWindow(w *MaintenanceWindowSpec) []error {
	if w == nil {
		return nil
	}
	var errs []error
	for i, d := range w.Days {
		if _, err := parseWeekday(d); err != nil {
			errs = append(errs, fmt.Errorf("spec.maintenanceWindow.days[%d]: %w", i, err))
		}
	}
	start, errStart := parseHHMM(w.Start)
	if errStart != nil {
		errs = append(errs, fmt.Errorf("spec.maintenanceWindow.start: %w", errStart))
	}
	end, errEnd := parseHHMM(w.End)
	if errEnd != nil {
		errs = append(errs, fmt.Errorf("spec.maintenanceWindow.end: %w", errEnd))
	}
	if errStart == nil && errEnd == nil && start == end {
		errs = append(errs, fmt.Errorf("spec.maintenanceWindow.start %q equals end %q: a window that opens and closes at the same time never opens (use 00:00-23:59 for all day)", w.Start, w.End))
	}
	return errs
}

func validateAvailability(spec ModelDeploymentSpec) []error {
	a := spec.Availability
	if a == nil {
		return nil
	}
	var errs []error
	replicas := int32(1)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	if a.MinAvailable < 0 {
		errs = append(errs, fmt.Errorf("spec.availability.minAvailable must be >= 0, got %d", a.MinAvailable))
	}
	if a.MinAvailable > 0 && a.MinAvailable >= replicas {
		errs = append(errs, fmt.Errorf("spec.availability.minAvailable (%d) must be < replicas (%d): minAvailable == replicas blocks every voluntary eviction", a.MinAvailable, replicas))
	}
	if a.MaxSkew < 0 || a.MaxSkew > 10 {
		errs = append(errs, fmt.Errorf("spec.availability.maxSkew must be between 0 and 10, got %d", a.MaxSkew))
	}
	return errs
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateSecurity checks the security block. A nil block is valid
// — ApplyDefaults fills it in before the spec is ever rendered.
// The checks are about contradictions and nonsense, not taste:
// taste is Lint's job. runAsUser 0 with runAsNonRoot on is a
// contradiction (the kubelet would refuse to start the pod);
// a capability name that is not a valid Linux capability token
// would be rejected by the API server at apply time; a seccomp
// profile that is neither RuntimeDefault, Unconfined nor a
// Localhost path is not a profile the kubelet knows.
func validateSecurity(spec ModelDeploymentSpec) []error {
	s := spec.Security
	if s == nil {
		return nil
	}
	var errs []error
	if s.RunAsUser != nil {
		if *s.RunAsUser < 0 || *s.RunAsUser > 4294967295 {
			errs = append(errs, fmt.Errorf("spec.security.runAsUser must be between 0 and 4294967295, got %d", *s.RunAsUser))
		}
		if *s.RunAsUser == 0 && s.RunAsNonRootEnabled() {
			errs = append(errs, fmt.Errorf("spec.security.runAsUser is 0 but runAsNonRoot is on: the kubelet would refuse to start the pod — set runAsNonRoot: false to run as root, or pick a non-zero uid"))
		}
	}
	for _, c := range s.DropCapabilities {
		if !capabilityRe.MatchString(c) {
			errs = append(errs, fmt.Errorf("spec.security.dropCapabilities entry %q is not a valid Linux capability name (uppercase letters, digits, '_', e.g. \"ALL\" or \"NET_BIND_SERVICE\")", c))
		}
	}
	if p := s.SeccompProfile; p != "" && p != "RuntimeDefault" && p != "Unconfined" && !strings.HasPrefix(p, "Localhost/") {
		errs = append(errs, fmt.Errorf("spec.security.seccompProfile %q must be \"RuntimeDefault\", \"Unconfined\", or \"Localhost/<profile>\"", p))
	}
	return errs
}

var capabilityRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// validatePlacement checks the placement block. A nil block is
// valid — unconstrained scheduling is a real choice, not an
// omission. The checks are about Kubernetes rules, not taste:
// a nodeSelector key that is not a label key would be rejected
// by the API server at apply time; a toleration with a value
// under operator Exists, or seconds without NoExecute, is
// likewise not a toleration the kubelet understands.
func validatePlacement(spec ModelDeploymentSpec) []error {
	p := spec.Placement
	if p == nil {
		return nil
	}
	var errs []error
	for k, v := range p.NodeSelector {
		if !isLabelKey(k) {
			errs = append(errs, fmt.Errorf("spec.placement.nodeSelector key %q is not a valid label key (optional DNS prefix + '/' + name of letters, digits, '-', '_', '.')", k))
		}
		if !isLabelValue(v) {
			errs = append(errs, fmt.Errorf("spec.placement.nodeSelector[%q] value %q is not a valid label value (at most 63 characters of letters, digits, '-', '_', '.')", k, v))
		}
	}
	for i := range p.Tolerations {
		errs = append(errs, validateToleration(p.Tolerations[i], fmt.Sprintf("spec.placement.tolerations[%d]", i))...)
	}
	return errs
}

// validateModelSource checks the model-source block. A nil
// block is valid — weights in the image is a real choice, not
// an omission. The checks are about what the init container
// can actually do: it downloads over plain HTTP(S) with curl,
// so the URI must be an http(s) URL (artifact stores that need
// credentials, like s3://, cannot be fetched and are rejected
// with an explanation rather than failing the pod at startup);
// a checksum, when given, must be a full 64-hex SHA-256 or the
// verification step could never pass; and the mount path must
// be absolute or the volume mount is meaningless.
func validateModelSource(spec ModelDeploymentSpec) []error {
	m := spec.ModelSource
	if m == nil {
		return nil
	}
	var errs []error
	uri := strings.TrimSpace(m.URI)
	if uri == "" {
		errs = append(errs, fmt.Errorf("spec.modelSource.uri must not be empty"))
	} else if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
		errs = append(errs, fmt.Errorf("spec.modelSource.uri %q must be an http(s) URL (artifact stores that need credentials, like s3://, are not supported)", m.URI))
	}
	if m.SHA256 != "" && !isSHA256Hex(m.SHA256) {
		errs = append(errs, fmt.Errorf("spec.modelSource.sha256 must be 64 hex characters, got %q", m.SHA256))
	}
	if m.MountPath != "" && !strings.HasPrefix(m.MountPath, "/") {
		errs = append(errs, fmt.Errorf("spec.modelSource.mountPath %q must be an absolute path", m.MountPath))
	}
	return errs
}

// isSHA256Hex reports whether s is exactly 64 hexadecimal
// characters — the length and alphabet of a SHA-256 digest.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// validateImagePullSecrets checks the pull-secret names. An
// empty list is valid — public images pull without
// credentials. Each name must be a DNS-1123 subdomain, the
// same rule Kubernetes applies to secret names: a bad name
// would be rejected by the API server at apply time, so the
// operator rejects it first. Duplicates are rejected too: the
// same secret twice is a typo, not a configuration.
func validateImagePullSecrets(spec ModelDeploymentSpec) []error {
	var errs []error
	seen := make(map[string]bool, len(spec.ImagePullSecrets))
	for i, name := range spec.ImagePullSecrets {
		path := fmt.Sprintf("spec.imagePullSecrets[%d]", i)
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("%s must not be empty", path))
			continue
		}
		if !isDNS1123Subdomain(name) {
			errs = append(errs, fmt.Errorf("%s %q must be a DNS-1123 subdomain (lowercase letters, digits, '-', '.')", path, name))
		}
		if seen[name] {
			errs = append(errs, fmt.Errorf("%s duplicates secret %q", path, name))
		}
		seen[name] = true
	}
	return errs
}

// validateMetadata checks the custom labels and annotations.
// Both maps share the key rules: a valid Kubernetes qualified
// name (optional DNS prefix + '/' + name). Label values follow
// the 63-character label rule; annotation values are free-form
// (a runbook URL, a long owner note), so any string is valid.
// Keys under the operator's own prefixes are rejected: the
// operator stamps app.kubernetes.io/name and ml-operator/*
// itself, and a user label colliding with one would silently
// overwrite the selector labels the Service and Deployments
// rely on. An empty map is valid — no custom metadata is a
// real choice, not an omission.
func validateMetadata(spec ModelDeploymentSpec) []error {
	var errs []error
	reserved := func(key string) string {
		for _, prefix := range []string{"app.kubernetes.io/", "ml-operator/"} {
			if strings.HasPrefix(key, prefix) {
				return prefix
			}
		}
		return ""
	}
	for k, v := range spec.Labels {
		path := fmt.Sprintf("spec.labels[%q]", k)
		if !isLabelKey(k) {
			errs = append(errs, fmt.Errorf("%s key %q is not a valid label key (optional DNS prefix + '/' + name of letters, digits, '-', '_', '.')", path, k))
		} else if prefix := reserved(k); prefix != "" {
			errs = append(errs, fmt.Errorf("%s key %q uses the reserved prefix %q (the operator owns those labels)", path, k, prefix))
		}
		if !isLabelValue(v) {
			errs = append(errs, fmt.Errorf("%s value %q is not a valid label value (at most 63 characters of letters, digits, '-', '_', '.')", path, v))
		}
	}
	for k := range spec.Annotations {
		path := fmt.Sprintf("spec.annotations[%q]", k)
		if !isLabelKey(k) {
			errs = append(errs, fmt.Errorf("%s key %q is not a valid annotation key (optional DNS prefix + '/' + name of letters, digits, '-', '_', '.')", path, k))
		} else if prefix := reserved(k); prefix != "" {
			errs = append(errs, fmt.Errorf("%s key %q uses the reserved prefix %q (the operator owns those annotations)", path, k, prefix))
		}
	}
	return errs
}

// validateToleration checks one toleration against the
// Kubernetes rules: operator Equal or Exists (empty means
// Equal), a value only under Equal, an effect from the known
// set (empty means any), and tolerationSeconds only with
// NoExecute. An empty key is legal only with Exists — it
// matches every taint — which is broad enough that Lint warns
// about it, but it is still a valid toleration.
func validateToleration(t Toleration, path string) []error {
	var errs []error
	op := t.Operator
	if op == "" {
		op = "Equal"
	}
	if op != "Equal" && op != "Exists" {
		errs = append(errs, fmt.Errorf("%s.operator %q must be \"Equal\" or \"Exists\"", path, t.Operator))
	}
	if op == "Exists" {
		if t.Value != "" {
			errs = append(errs, fmt.Errorf("%s.value must be empty when operator is \"Exists\" (an Exists toleration matches on the key alone)", path))
		}
		if t.Key != "" && !isLabelKey(t.Key) {
			errs = append(errs, fmt.Errorf("%s.key %q is not a valid label key (optional DNS prefix + '/' + name of letters, digits, '-', '_', '.')", path, t.Key))
		}
	} else {
		if t.Key == "" {
			errs = append(errs, fmt.Errorf("%s.key must not be empty when operator is \"Equal\" (use operator \"Exists\" to match every taint)", path))
		} else if !isLabelKey(t.Key) {
			errs = append(errs, fmt.Errorf("%s.key %q is not a valid label key (optional DNS prefix + '/' + name of letters, digits, '-', '_', '.')", path, t.Key))
		}
		if !isLabelValue(t.Value) {
			errs = append(errs, fmt.Errorf("%s.value %q is not a valid label value (at most 63 characters of letters, digits, '-', '_', '.')", path, t.Value))
		}
	}
	switch t.Effect {
	case "", "NoSchedule", "PreferNoSchedule", "NoExecute":
	default:
		errs = append(errs, fmt.Errorf("%s.effect %q must be \"NoSchedule\", \"PreferNoSchedule\", \"NoExecute\", or empty (any effect)", path, t.Effect))
	}
	if t.TolerationSeconds != nil {
		if t.Effect != "NoExecute" {
			errs = append(errs, fmt.Errorf("%s.tolerationSeconds is only meaningful with effect \"NoExecute\" (got %q)", path, t.Effect))
		}
		if *t.TolerationSeconds < 0 {
			errs = append(errs, fmt.Errorf("%s.tolerationSeconds must be >= 0, got %d", path, *t.TolerationSeconds))
		}
	}
	return errs
}

// isLabelKey reports whether s is a valid Kubernetes label key:
// an optional DNS-1123 subdomain prefix, a "/", and a name of
// at most 63 characters (letters, digits, '-', '_', '.'), or a
// bare name with no prefix.
func isLabelKey(s string) bool {
	name := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		if !isDNS1123Subdomain(s[:i]) {
			return false
		}
		name = s[i+1:]
	}
	return labelNameRe.MatchString(name)
}

// isLabelValue reports whether s is a valid Kubernetes label
// value: empty, or at most 63 characters starting and ending
// with a letter or digit.
func isLabelValue(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 63 {
		return false
	}
	return labelNameRe.MatchString(s)
}

var labelNameRe = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

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
