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
