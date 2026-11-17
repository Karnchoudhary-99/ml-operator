package manifest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// Parse parses a manifest document — YAML (the documented subset) or
// JSON — into a ModelDeployment. Generation and status are never read
// from a manifest: the CLI assigns the generation when it applies.
// Unknown keys are an error, so a typo fails loudly instead of being
// silently ignored.
func Parse(data []byte) (api.ModelDeployment, error) {
	var d api.ModelDeployment
	tree, err := ParseTree(data)
	if err != nil {
		return d, err
	}
	if err := checkKeys(tree, []string{"name", "namespace", "spec"}, ""); err != nil {
		return d, err
	}
	if err := mapDeployment(tree, &d); err != nil {
		return d, err
	}
	return d, nil
}

// ParseTree parses a document — YAML (the documented subset) or JSON —
// into a nested key tree. Other packages (operator configuration)
// reuse it so the project has one YAML-subset parser, not two.
func ParseTree(data []byte) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("document is empty")
	}
	var tree map[string]any
	var err error
	if strings.HasPrefix(trimmed, "{") {
		err = json.Unmarshal(data, &tree)
		if err != nil {
			return nil, fmt.Errorf("invalid JSON document: %w", err)
		}
	} else {
		tree, err = parseYAMLSubset(data)
		if err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// mapDeployment fills a ModelDeployment from the parsed key tree.
func mapDeployment(tree map[string]any, d *api.ModelDeployment) error {
	name, ok, err := strField(tree, "name", "")
	if err != nil {
		return err
	}
	if ok {
		d.Name = name
	}
	ns, ok, err := strField(tree, "namespace", "")
	if err != nil {
		return err
	}
	if ok {
		d.Namespace = ns
	}
	specRaw, ok := tree["spec"]
	if !ok {
		return nil
	}
	spec, ok := specRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("spec: must be a mapping")
	}
	return mapSpec(spec, &d.Spec)
}

func mapSpec(spec map[string]any, s *api.ModelDeploymentSpec) error {
	if err := checkKeys(spec, []string{
		"model", "replicas", "resources", "port", "strategy",
		"canary", "blueGreen", "autoscaling", "availability", "security", "placement", "env", "health",
		"paused", "maintenanceWindow",
	}, "spec"); err != nil {
		return err
	}

	if raw, ok := spec["model"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.model: must be a mapping")
		}
		if err := checkKeys(m, []string{"image", "framework", "version"}, "spec.model"); err != nil {
			return err
		}
		if v, ok, err := strField(m, "image", "spec.model"); err != nil {
			return err
		} else if ok {
			s.Model.Image = v
		}
		if v, ok, err := strField(m, "framework", "spec.model"); err != nil {
			return err
		} else if ok {
			s.Model.Framework = v
		}
		if v, ok, err := strField(m, "version", "spec.model"); err != nil {
			return err
		} else if ok {
			s.Model.Version = v
		}
	}

	if raw, ok := spec["replicas"]; ok {
		n, err := intField(raw, "spec.replicas")
		if err != nil {
			return err
		}
		v := int32(n)
		s.Replicas = &v
	}

	if raw, ok := spec["resources"]; ok {
		r, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.resources: must be a mapping")
		}
		if err := checkKeys(r, []string{"cpuRequest", "cpuLimit", "memoryRequest", "memoryLimit"}, "spec.resources"); err != nil {
			return err
		}
		if v, ok, err := strField(r, "cpuRequest", "spec.resources"); err != nil {
			return err
		} else if ok {
			s.Resources.CPURequest = v
		}
		if v, ok, err := strField(r, "cpuLimit", "spec.resources"); err != nil {
			return err
		} else if ok {
			s.Resources.CPULimit = v
		}
		if v, ok, err := strField(r, "memoryRequest", "spec.resources"); err != nil {
			return err
		} else if ok {
			s.Resources.MemoryRequest = v
		}
		if v, ok, err := strField(r, "memoryLimit", "spec.resources"); err != nil {
			return err
		} else if ok {
			s.Resources.MemoryLimit = v
		}
	}

	if raw, ok := spec["port"]; ok {
		n, err := intField(raw, "spec.port")
		if err != nil {
			return err
		}
		s.Port = int32(n)
	}

	if raw, ok := spec["paused"]; ok {
		b, err := boolField(raw, "spec.paused")
		if err != nil {
			return err
		}
		s.Paused = b
	}

	if raw, ok := spec["maintenanceWindow"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.maintenanceWindow: must be a mapping")
		}
		if err := checkKeys(m, []string{"days", "start", "end"}, "spec.maintenanceWindow"); err != nil {
			return err
		}
		w := &api.MaintenanceWindowSpec{}
		if v, ok := m["days"]; ok {
			days, err := stringListField(v, "spec.maintenanceWindow.days")
			if err != nil {
				return err
			}
			w.Days = days
		}
		if v, ok, err := strField(m, "start", "spec.maintenanceWindow"); err != nil {
			return err
		} else if ok {
			w.Start = v
		}
		if v, ok, err := strField(m, "end", "spec.maintenanceWindow"); err != nil {
			return err
		} else if ok {
			w.End = v
		}
		s.MaintenanceWindow = w
	}

	if v, ok, err := strField(spec, "strategy", "spec"); err != nil {
		return err
	} else if ok {
		s.Strategy = api.RolloutStrategy(v)
	}

	if raw, ok := spec["canary"]; ok {
		c, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.canary: must be a mapping")
		}
		if err := checkKeys(c, []string{"weight", "steps", "intervalSeconds", "gate"}, "spec.canary"); err != nil {
			return err
		}
		cs := &api.CanarySpec{}
		if v, ok := c["weight"]; ok {
			n, err := intField(v, "spec.canary.weight")
			if err != nil {
				return err
			}
			cs.Weight = int32(n)
		}
		if v, ok := c["steps"]; ok {
			n, err := intField(v, "spec.canary.steps")
			if err != nil {
				return err
			}
			cs.Steps = int32(n)
		}
		if v, ok := c["intervalSeconds"]; ok {
			n, err := intField(v, "spec.canary.intervalSeconds")
			if err != nil {
				return err
			}
			cs.IntervalSeconds = int32(n)
		}
		if raw, ok := c["gate"]; ok {
			gs, err := parseGate(raw, "spec.canary.gate")
			if err != nil {
				return err
			}
			cs.Gate = gs
		}
		s.Canary = cs
	}

	if raw, ok := spec["blueGreen"]; ok {
		b, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.blueGreen: must be a mapping")
		}
		if err := checkKeys(b, []string{"gate"}, "spec.blueGreen"); err != nil {
			return err
		}
		bs := &api.BlueGreenSpec{}
		if raw, ok := b["gate"]; ok {
			gs, err := parseGate(raw, "spec.blueGreen.gate")
			if err != nil {
				return err
			}
			bs.Gate = gs
		}
		s.BlueGreen = bs
	}

	if raw, ok := spec["autoscaling"]; ok {
		a, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.autoscaling: must be a mapping")
		}
		if err := checkKeys(a, []string{"minReplicas", "maxReplicas", "targetCPUUtilization", "targetRequestsPerSecond"}, "spec.autoscaling"); err != nil {
			return err
		}
		as := &api.AutoscalingSpec{}
		if v, ok := a["minReplicas"]; ok {
			n, err := intField(v, "spec.autoscaling.minReplicas")
			if err != nil {
				return err
			}
			as.MinReplicas = int32(n)
		}
		if v, ok := a["maxReplicas"]; ok {
			n, err := intField(v, "spec.autoscaling.maxReplicas")
			if err != nil {
				return err
			}
			as.MaxReplicas = int32(n)
		}
		if v, ok := a["targetCPUUtilization"]; ok {
			n, err := intField(v, "spec.autoscaling.targetCPUUtilization")
			if err != nil {
				return err
			}
			as.TargetCPUUtilization = int32(n)
		}
		if v, ok := a["targetRequestsPerSecond"]; ok {
			n, err := intField(v, "spec.autoscaling.targetRequestsPerSecond")
			if err != nil {
				return err
			}
			as.TargetRequestsPerSecond = int32(n)
		}
		s.Autoscaling = as
	}

	if raw, ok := spec["availability"]; ok {
		a, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.availability: must be a mapping")
		}
		if err := checkKeys(a, []string{"minAvailable", "antiAffinity", "maxSkew"}, "spec.availability"); err != nil {
			return err
		}
		as := &api.AvailabilitySpec{}
		if v, ok := a["minAvailable"]; ok {
			n, err := intField(v, "spec.availability.minAvailable")
			if err != nil {
				return err
			}
			as.MinAvailable = int32(n)
		}
		if v, ok := a["antiAffinity"]; ok {
			b, err := boolField(v, "spec.availability.antiAffinity")
			if err != nil {
				return err
			}
			as.AntiAffinity = &b
		}
		if v, ok := a["maxSkew"]; ok {
			n, err := intField(v, "spec.availability.maxSkew")
			if err != nil {
				return err
			}
			as.MaxSkew = int32(n)
		}
		s.Availability = as
	}

	if raw, ok := spec["security"]; ok {
		a, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.security: must be a mapping")
		}
		if err := checkKeys(a, []string{"runAsNonRoot", "runAsUser", "readOnlyRootFilesystem", "allowPrivilegeEscalation", "dropCapabilities", "seccompProfile"}, "spec.security"); err != nil {
			return err
		}
		ss := &api.SecuritySpec{}
		if v, ok := a["runAsNonRoot"]; ok {
			b, err := boolField(v, "spec.security.runAsNonRoot")
			if err != nil {
				return err
			}
			ss.RunAsNonRoot = &b
		}
		if v, ok := a["runAsUser"]; ok {
			n, err := intField(v, "spec.security.runAsUser")
			if err != nil {
				return err
			}
			ss.RunAsUser = &n
		}
		if v, ok := a["readOnlyRootFilesystem"]; ok {
			b, err := boolField(v, "spec.security.readOnlyRootFilesystem")
			if err != nil {
				return err
			}
			ss.ReadOnlyRootFilesystem = &b
		}
		if v, ok := a["allowPrivilegeEscalation"]; ok {
			b, err := boolField(v, "spec.security.allowPrivilegeEscalation")
			if err != nil {
				return err
			}
			ss.AllowPrivilegeEscalation = &b
		}
		if v, ok := a["dropCapabilities"]; ok {
			caps, err := stringListField(v, "spec.security.dropCapabilities")
			if err != nil {
				return err
			}
			ss.DropCapabilities = caps
		}
		if v, ok, err := strField(a, "seccompProfile", "spec.security"); err != nil {
			return err
		} else if ok {
			ss.SeccompProfile = v
		}
		s.Security = ss
	}

	if raw, ok := spec["placement"]; ok {
		a, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.placement: must be a mapping")
		}
		if err := checkKeys(a, []string{"nodeSelector", "tolerations"}, "spec.placement"); err != nil {
			return err
		}
		ps := &api.PlacementSpec{}
		if v, ok := a["nodeSelector"]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("spec.placement.nodeSelector: must be a mapping")
			}
			sel := make(map[string]string, len(m))
			for k, val := range m {
				sv, ok := val.(string)
				if !ok {
					return fmt.Errorf("spec.placement.nodeSelector.%s: must be a string", k)
				}
				sel[k] = sv
			}
			ps.NodeSelector = sel
		}
		if v, ok := a["tolerations"]; ok {
			tols, err := tolerationsField(v, "spec.placement.tolerations")
			if err != nil {
				return err
			}
			ps.Tolerations = tols
		}
		s.Placement = ps
	}

	if raw, ok := spec["env"]; ok {
		e, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.env: must be a mapping")
		}
		env := make(map[string]string, len(e))
		for k, v := range e {
			sv, ok := v.(string)
			if !ok {
				return fmt.Errorf("spec.env.%s: must be a string", k)
			}
			env[k] = sv
		}
		s.Env = env
	}

	if raw, ok := spec["health"]; ok {
		h, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.health: must be a mapping")
		}
		if err := checkKeys(h, []string{"path", "initialDelaySeconds", "periodSeconds"}, "spec.health"); err != nil {
			return err
		}
		if v, ok, err := strField(h, "path", "spec.health"); err != nil {
			return err
		} else if ok {
			s.Health.Path = v
		}
		if v, ok := h["initialDelaySeconds"]; ok {
			n, err := intField(v, "spec.health.initialDelaySeconds")
			if err != nil {
				return err
			}
			s.Health.InitialDelaySeconds = int32(n)
		}
		if v, ok := h["periodSeconds"]; ok {
			n, err := intField(v, "spec.health.periodSeconds")
			if err != nil {
				return err
			}
			s.Health.PeriodSeconds = int32(n)
		}
	}
	return nil
}

// parseGate reads a gate mapping (maxErrorRate, maxP95LatencyMs,
// minReadyRatio) shared by the canary and blue-green blocks.
func parseGate(raw any, path string) (*api.GateSpec, error) {
	g, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: must be a mapping", path)
	}
	if err := checkKeys(g, []string{"maxErrorRate", "maxP95LatencyMs", "minReadyRatio"}, path); err != nil {
		return nil, err
	}
	gs := &api.GateSpec{}
	if v, ok := g["maxErrorRate"]; ok {
		f, err := floatField(v, path+".maxErrorRate")
		if err != nil {
			return nil, err
		}
		gs.MaxErrorRate = f
	}
	if v, ok := g["maxP95LatencyMs"]; ok {
		f, err := floatField(v, path+".maxP95LatencyMs")
		if err != nil {
			return nil, err
		}
		gs.MaxP95LatencyMs = f
	}
	if v, ok := g["minReadyRatio"]; ok {
		f, err := floatField(v, path+".minReadyRatio")
		if err != nil {
			return nil, err
		}
		gs.MinReadyRatio = f
	}
	return gs, nil
}

// strField reads a string field: ok is false when the key is absent.
func strField(m map[string]any, key, path string) (string, bool, error) {
	raw, ok := m[key]
	if !ok {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s.%s: must be a string", path, key)
	}
	return s, true, nil
}

// intField reads an integer field. YAML ints arrive as int64, JSON
// numbers as float64; both are accepted when they are whole numbers.
func intField(raw any, path string) (int64, error) {
	switch v := raw.(type) {
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("%s: must be a whole number", path)
		}
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: must be a number, got %q", path, v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s: must be a number", path)
	}
}

// boolField reads a boolean field. YAML booleans arrive as bool;
// the strings "true"/"false" are accepted too, so JSON-style
// quoted booleans work.
func boolField(raw any, path string) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("%s: must be a boolean, got %q", path, v)
		}
		return b, nil
	default:
		return false, fmt.Errorf("%s: must be a boolean", path)
	}
}

// tolerationsField parses the tolerations of a placement block.
// JSON manifests carry a real array of objects; the YAML dialect
// has no lists, so a YAML manifest writes a comma-separated string
// of compact entries, one per toleration:
//
//	"gpu=true:NoSchedule, dedicated:Exists:NoExecute:300"
//
// Each entry is key[=value][:Exists][:effect][:seconds]: an "="
// means operator Equal, a ":Exists" part means operator Exists
// (and the value must be absent), the effect is NoSchedule,
// PreferNoSchedule or NoExecute, and the seconds are only
// meaningful with NoExecute — which also needs the effect in the
// compact form ("key=value:300" cannot say which effect the
// seconds belong to). It is the same compact form api.Toleration
// renders with String(), so `mlop status` output pastes straight
// back into a manifest.
func tolerationsField(raw any, path string) ([]api.Toleration, error) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return []api.Toleration{}, nil
		}
		parts := strings.Split(v, ",")
		out := make([]api.Toleration, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return nil, fmt.Errorf("%s: empty entry in %q", path, v)
			}
			tol, err := parseCompactToleration(p, path)
			if err != nil {
				return nil, err
			}
			out = append(out, tol)
		}
		return out, nil
	case []any:
		out := make([]api.Toleration, 0, len(v))
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: must be a mapping", path, i)
			}
			tol, err := tolerationFromMap(m, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, tol)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: must be a comma-separated string or a list of toleration objects", path)
	}
}

// parseCompactToleration parses one compact toleration entry:
// key[=value][:Exists][:effect][:seconds].
func parseCompactToleration(entry, path string) (api.Toleration, error) {
	var tol api.Toleration
	parts := strings.Split(entry, ":")
	if len(parts) > 4 {
		return tol, fmt.Errorf("%s: toleration %q has too many ':'-separated parts (want key[=value][:Exists][:effect][:seconds])", path, entry)
	}
	keyVal := parts[0]
	if i := strings.Index(keyVal, "="); i >= 0 {
		tol.Key = keyVal[:i]
		tol.Value = keyVal[i+1:]
		tol.Operator = "Equal"
	} else {
		tol.Key = keyVal
	}
	idx := 1
	if idx < len(parts) && parts[idx] == "Exists" {
		tol.Operator = "Exists"
		idx++
	}
	if idx < len(parts) {
		tol.Effect = parts[idx]
		idx++
	}
	if idx < len(parts) {
		n, err := strconv.ParseInt(parts[idx], 10, 64)
		if err != nil {
			return tol, fmt.Errorf("%s: toleration %q has non-numeric tolerationSeconds %q", path, entry, parts[idx])
		}
		tol.TolerationSeconds = &n
		idx++
	}
	return tol, nil
}

// tolerationFromMap fills one toleration from a JSON object:
// key, operator, value, effect, tolerationSeconds.
func tolerationFromMap(m map[string]any, path string) (api.Toleration, error) {
	var tol api.Toleration
	if err := checkKeys(m, []string{"key", "operator", "value", "effect", "tolerationSeconds"}, path); err != nil {
		return tol, err
	}
	if v, ok, err := strField(m, "key", path); err != nil {
		return tol, err
	} else if ok {
		tol.Key = v
	}
	if v, ok, err := strField(m, "operator", path); err != nil {
		return tol, err
	} else if ok {
		tol.Operator = v
	}
	if v, ok, err := strField(m, "value", path); err != nil {
		return tol, err
	} else if ok {
		tol.Value = v
	}
	if v, ok, err := strField(m, "effect", path); err != nil {
		return tol, err
	} else if ok {
		tol.Effect = v
	}
	if v, ok := m["tolerationSeconds"]; ok {
		n, err := intField(v, path+".tolerationSeconds")
		if err != nil {
			return tol, err
		}
		tol.TolerationSeconds = &n
	}
	return tol, nil
}

// stringListField parses a list of strings. JSON manifests carry a
// real array; the YAML dialect has no lists, so a YAML manifest
// writes a comma-separated string ("Mon, Wed, Fri"). An empty
// value parses to an empty list.
func stringListField(raw any, path string) ([]string, error) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return []string{}, nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return nil, fmt.Errorf("%s: empty entry in %q", path, v)
			}
			out = append(out, p)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: every entry must be a string", path)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: must be a comma-separated string or a list of strings", path)
	}
}

// floatField reads a fractional-number field. YAML floats arrive as
// float64, YAML/JSON whole numbers as int64 or float64; all are
// accepted. Strings parse as floats, so JSON-style "0.05" works too.
func floatField(raw any, path string) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: must be a number, got %q", path, v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%s: must be a number", path)
	}
}

// checkKeys rejects keys outside the known set for a mapping.
func checkKeys(m map[string]any, known []string, path string) error {
	allowed := make(map[string]bool, len(known))
	for _, k := range known {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			if path == "" {
				return fmt.Errorf("unknown key %q", k)
			}
			return fmt.Errorf("%s: unknown key %q", path, k)
		}
	}
	return nil
}

// parseYAMLSubset parses the documented YAML subset into a nested
// map: indentation-based mappings, scalar values, "#" comments,
// blank lines. Lists, anchors, multi-line scalars and flow maps are
// out of scope and rejected with a line number.
func parseYAMLSubset(data []byte) (map[string]any, error) {
	root := map[string]any{}
	// stack of (indent, map) pairs; the root sits at indent -1.
	type frame struct {
		indent int
		m      map[string]any
	}
	stack := []frame{{-1, root}}
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		lineno := i + 1
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "\t") || strings.Contains(line, "\t") {
			return nil, fmt.Errorf("line %d: tabs are not allowed, use spaces", lineno)
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			return nil, fmt.Errorf("line %d: lists are not supported in manifests", lineno)
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return nil, fmt.Errorf("line %d: flow collections are not supported in manifests", lineno)
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected 'key: value'", lineno)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineno)
		}
		if strings.ContainsAny(key, " \t\"'") {
			return nil, fmt.Errorf("line %d: invalid key %q", lineno, key)
		}
		// Pop frames until the parent is the nearest one with a
		// strictly smaller indent.
		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].m
		if _, exists := parent[key]; exists {
			return nil, fmt.Errorf("line %d: duplicate key %q", lineno, key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			// Nested mapping: the following deeper-indented lines
			// fill it in.
			child := map[string]any{}
			parent[key] = child
			stack = append(stack, frame{indent, child})
			continue
		}
		v, err := parseScalar(value)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineno, err)
		}
		parent[key] = v
	}
	return root, nil
}

// stripComment removes a trailing " # comment" (or a whole-line
// comment). A "#" inside quotes is part of the value.
func stripComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
					return line[:i]
				}
			}
		}
	}
	return line
}

// parseScalar parses one YAML scalar: quoted strings, integers,
// booleans, or a plain string.
func parseScalar(s string) (any, error) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		v, err := strconv.Unquote(s)
		if err != nil {
			return nil, fmt.Errorf("invalid double-quoted string %q", s)
		}
		return v, nil
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if strings.ContainsAny(s, "{}[]&*!|>%@`") {
		return nil, fmt.Errorf("unsupported value %q", s)
	}
	return s, nil
}
