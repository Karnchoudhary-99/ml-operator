// Lint reports warnings for specs that are valid but risky. Unlike
// Validate, which rejects a spec outright, Lint flags choices the
// operator will accept but the author probably did not mean: an
// untagged image that silently floats, a canary that sends most
// traffic to an unproven revision on its first step, a gate that
// can promote with pods still unready.
//
// Lint inspects the deployment as written, BEFORE ApplyDefaults.
// Defaults would hide the author's explicit choices: an explicit
// minReplicas 0 becomes 1, an unset resource limit becomes 1Gi,
// so linting after defaulting could not tell a deliberate value
// from a filled-in one. Callers run Lint on the parsed manifest
// and Validate on the defaulted deployment.
package api

import (
	"fmt"
	"strings"
)

// Lint returns every warning for the deployment's spec, or nil when
// there is nothing worth flagging. Warnings never fail admission on
// their own; `mlop validate --strict` turns them into errors.
func Lint(d *ModelDeployment) []string {
	var warns []string
	add := func(format string, args ...any) {
		warns = append(warns, fmt.Sprintf(format, args...))
	}

	spec := d.Spec
	warns = append(warns, lintImage(spec.Model.Image)...)
	if strings.TrimSpace(spec.Model.Framework) == "" {
		add("spec.model.framework is empty; the framework is recorded for observability and picks the Triton health path")
	}
	if spec.Replicas != nil && *spec.Replicas == 0 && spec.Autoscaling == nil {
		add("spec.replicas is 0 and autoscaling is off, so nothing will serve")
	}
	if spec.Autoscaling != nil && spec.Autoscaling.MinReplicas == 0 {
		add("spec.autoscaling.minReplicas is 0, but the operator raises it to 1: this is not scale-to-zero")
	}
	if spec.Paused {
		add("spec.paused is true; the operator will not reconcile this deployment until it is resumed")
	}
	warns = append(warns, lintAvailability(spec)...)
	warns = append(warns, lintMaintenanceWindow(spec.MaintenanceWindow)...)
	warns = append(warns, lintSecurity(spec)...)
	warns = append(warns, lintPlacement(spec.Placement)...)
	if spec.Strategy == StrategyCanary && spec.Canary != nil {
		if spec.Canary.Weight > 50 {
			add("spec.canary.weight is %d%%: the first canary step sends most traffic to an unproven revision", spec.Canary.Weight)
		}
		if spec.Canary.IntervalSeconds > 600 {
			add("spec.canary.intervalSeconds is %ds: each canary step holds traffic for over 10 minutes", spec.Canary.IntervalSeconds)
		}
		warns = append(warns, lintGate(spec.Canary.Gate, "spec.canary.gate")...)
	}
	if spec.Strategy == StrategyBlueGreen {
		warns = append(warns, lintGate(gateOf(spec.BlueGreen), "spec.blueGreen.gate")...)
	}
	if len(warns) == 0 {
		return nil
	}
	return warns
}

// lintImage warns about image references that make rollouts
// unreliable. An untagged image resolves to :latest, and :latest
// itself never changes from the operator's point of view: two
// different binaries can share one tag, so a re-apply looks like
// a no-op and a rollback cannot tell the versions apart.
func lintImage(image string) []string {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil
	}
	// The tag is the part after the last ':', but only when it
	// comes after the last '/' (a registry port like
	// localhost:5000/img has a ':' that is not a tag).
	afterSlash := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		afterSlash = image[i+1:]
	}
	if !strings.Contains(afterSlash, ":") {
		return []string{fmt.Sprintf("spec.model.image %q has no tag and resolves to :latest; pin an explicit tag so a re-apply is not a silent no-op", image)}
	}
	if strings.HasSuffix(afterSlash, ":latest") {
		return []string{fmt.Sprintf("spec.model.image %q uses the :latest tag; two different binaries can share it, so rollouts and rollbacks cannot tell versions apart", image)}
	}
	return nil
}

// lintAvailability warns about availability choices that leave a
// multi-replica model exposed to a single node loss. An unset
// availability block is clean: ApplyDefaults turns anti-affinity
// on and maxSkew to 1, so only explicit choices warn.
func lintAvailability(spec ModelDeploymentSpec) []string {
	a := spec.Availability
	if a == nil {
		return nil
	}
	var warns []string
	replicas := int32(1)
	if spec.Replicas != nil {
		replicas = *spec.Replicas
	}
	if a.AntiAffinity != nil && !*a.AntiAffinity && replicas > 1 {
		warns = append(warns, fmt.Sprintf(
			"spec.availability.antiAffinity is false with %d replicas: pods of one model may land on a single node, so one node loss takes the whole model down", replicas))
	}
	if a.MaxSkew > 1 && replicas > 1 && a.MaxSkew >= replicas {
		warns = append(warns, fmt.Sprintf(
			"spec.availability.maxSkew is %d with %d replicas: the spread constraint allows every pod on one node", a.MaxSkew, replicas))
	}
	return warns
}

// lintMaintenanceWindow warns about a window so short that a
// rollout may not fit inside it. A canary step needs its interval
// plus a health-gate evaluation; a 5-minute window that opens at
// 02:00 will strand a rollout mid-step at 02:05, and the next
// step waits a whole day. The window is still valid — short
// windows are a deliberate choice for some teams — it just should
// not happen by accident.
func lintMaintenanceWindow(w *MaintenanceWindowSpec) []string {
	if w == nil {
		return nil
	}
	start, errStart := parseHHMM(w.Start)
	end, errEnd := parseHHMM(w.End)
	if errStart != nil || errEnd != nil {
		return nil
	}
	if dur := windowDurationMinutes(start, end); dur < 30 {
		return []string{fmt.Sprintf(
			"spec.maintenanceWindow is only %d minutes long: a canary step plus its health gate may not fit inside it, stranding the rollout until the next window", dur)}
	}
	return nil
}

// lintGate warns about a gate that can promote a rollout before
// the new revision is fully ready. A zero ratio means "use the
// operator default" (1.0), never "disable the check", so only an
// explicitly-set ratio below 1.0 is flagged.
func lintGate(g *GateSpec, path string) []string {
	if g == nil {
		return nil
	}
	if g.MinReadyRatio > 0 && g.MinReadyRatio < 1 {
		return []string{fmt.Sprintf(
			"%s.minReadyRatio is %g: the rollout can promote while some pods are still unready", path, g.MinReadyRatio)}
	}
	return nil
}

// lintPlacement warns about placement choices that leave a
// model unscheduled or evicted by surprise. An unset placement
// block is clean: unconstrained scheduling is the default, so
// only explicit choices warn.
func lintPlacement(p *PlacementSpec) []string {
	if p == nil {
		return nil
	}
	var warns []string
	for _, t := range p.Tolerations {
		op := t.Operator
		if op == "" {
			op = "Equal"
		}
		if op == "Exists" && t.Key == "" {
			warns = append(warns, "spec.placement.tolerations has an Exists toleration with an empty key: the pod tolerates every taint on every node, so node taints stop protecting other workloads from it")
		}
		if t.Effect == "NoExecute" && t.TolerationSeconds == nil {
			warns = append(warns, fmt.Sprintf(
				"spec.placement.tolerations %q has effect NoExecute with no tolerationSeconds: the pod is evicted immediately when the taint appears, with no grace period", t.String()))
		}
	}
	return warns
}

// lintSecurity warns about security choices that give the model
// server more privilege than it needs. An unset security block
// is clean: ApplyDefaults hardens it, so only explicit
// relaxations warn. Each warning names the risk, because a
// relaxation with a reason (a server that genuinely needs a
// writable rootfs) is fine — it just should not happen silently.
func lintSecurity(spec ModelDeploymentSpec) []string {
	s := spec.Security
	if s == nil {
		return nil
	}
	var warns []string
	if s.RunAsNonRoot != nil && !*s.RunAsNonRoot {
		warns = append(warns, "spec.security.runAsNonRoot is false: the model container may run as root — a compromised server has host-level privilege")
	}
	if s.RunAsUser != nil && *s.RunAsUser == 0 {
		warns = append(warns, "spec.security.runAsUser is 0: the model container runs as root")
	}
	if s.ReadOnlyRootFilesystem != nil && !*s.ReadOnlyRootFilesystem {
		warns = append(warns, "spec.security.readOnlyRootFilesystem is false: a compromised model server can write its own binary or drop a persistence file")
	}
	if s.AllowPrivilegeEscalation != nil && *s.AllowPrivilegeEscalation {
		warns = append(warns, "spec.security.allowPrivilegeEscalation is true: a process in the model container can gain more privileges than its parent")
	}
	if s.DropCapabilities != nil && len(s.DropCapabilities) == 0 {
		warns = append(warns, "spec.security.dropCapabilities is empty: no Linux capabilities are dropped from the model container")
	}
	if s.SeccompProfile == "Unconfined" {
		warns = append(warns, "spec.security.seccompProfile is Unconfined: the model container runs with no syscall filtering")
	}
	return warns
}
