package rollout

import "fmt"

// Gate is the health gate a canary step must pass before the rollout
// advances. Thresholds set to 0 are skipped, except MinReadyRatio,
// which defaults to 1.0 (every canary pod ready) — a canary that is
// not fully ready has not proven itself yet. The readiness check is
// always available (the cluster reports it); error rate, latency and
// the model-load probe are filled from the cluster metrics source
// when one reports them, and skipped otherwise.
type Gate struct {
	// MinReadyRatio is the minimum ready/desired canary replicas
	// (0-1). 0 means "all ready".
	MinReadyRatio float64
	// MaxErrorRate is the maximum tolerated error fraction (0-1).
	// 0 disables the check.
	MaxErrorRate float64
	// MaxLatencyP95Ms is the maximum tolerated p95 latency in
	// milliseconds. 0 disables the check.
	MaxLatencyP95Ms float64
}

// Sample is one health observation of the canary.
type Sample struct {
	ReadyReplicas   int32
	DesiredReplicas int32
	// ErrorRate is the fraction of failed requests (0-1). -1 means
	// "no metrics source", the same as 0 for evaluation purposes.
	ErrorRate float64
	// LatencyP95Ms is the p95 request latency in milliseconds. 0
	// means "no metrics source".
	LatencyP95Ms float64
	// ModelLoadKnown is true when the cluster reported whether the
	// model finished loading (the model-load probe: the serving
	// container's readiness endpoint for the model itself, e.g.
	// Triton's /v2/models/<name>/ready). False means the probe has
	// not reported yet, and the check is skipped.
	ModelLoadKnown bool
	// ModelLoaded is true when the model finished loading into the
	// serving container. A canary whose model is not loaded yet has
	// not proven itself, whatever its pod readiness says.
	ModelLoaded bool
	// ModelLoadFailed is true when the probe reported that the model
	// failed to load (bad artifact, OOM while loading, missing
	// weights). Unlike "not loaded yet", a failed load is a real
	// failure: it burns failure budget and can abort the rollout.
	// It only means something when ModelLoadKnown is true.
	ModelLoadFailed bool
}

// Evaluation is the outcome of checking a Sample against a Gate.
type Evaluation struct {
	// Pass is true when the gate is satisfied and the rollout may
	// advance (subject to the step interval).
	Pass bool
	// Pending is true when the sample cannot judge the canary yet:
	// no canary pod is ready, so the pods are still starting. A
	// pending evaluation is a hold, never a failure — a fresh canary
	// must not burn its failure budget while its pods come up.
	Pending bool
	// Reasons names every failed check, in evaluation order.
	Reasons []string
}

// Evaluate checks s against g. A zero DesiredReplicas is pending
// (there is nothing to judge); a zero ready count is pending too.
// Error-rate and latency checks run only when the gate sets a
// threshold and the sample carries a value.
func Evaluate(g Gate, s Sample) Evaluation {
	minReady := g.MinReadyRatio
	if minReady <= 0 {
		minReady = 1.0
	}
	if s.DesiredReplicas <= 0 {
		return Evaluation{Pending: true, Reasons: []string{"no desired canary replicas"}}
	}
	if s.ReadyReplicas <= 0 {
		return Evaluation{Pending: true, Reasons: []string{
			fmt.Sprintf("0/%d canary replicas ready (pods still starting)", s.DesiredReplicas),
		}}
	}
	// The model-load probe: a canary whose model has not loaded yet
	// is pending, not failed — the pods are up but the model is
	// still warming up, so the gate must not burn failure budget.
	// A canary whose model FAILED to load is a real failure: the
	// rollout should hold briefly, then abort, not wait forever
	// for a model that will never load.
	if s.ModelLoadKnown && s.ModelLoadFailed {
		return Evaluation{Pass: false, Reasons: []string{"model failed to load (model-load probe)"}}
	}
	if s.ModelLoadKnown && !s.ModelLoaded {
		return Evaluation{Pending: true, Reasons: []string{"model not loaded yet (model-load probe)"}}
	}
	ev := Evaluation{Pass: true}
	ratio := float64(s.ReadyReplicas) / float64(s.DesiredReplicas)
	if ratio < minReady {
		ev.Pass = false
		ev.Reasons = append(ev.Reasons,
			fmt.Sprintf("ready ratio %.2f below minimum %.2f (%d/%d ready)",
				ratio, minReady, s.ReadyReplicas, s.DesiredReplicas))
	}
	if g.MaxErrorRate > 0 && s.ErrorRate >= 0 && s.ErrorRate > g.MaxErrorRate {
		ev.Pass = false
		ev.Reasons = append(ev.Reasons,
			fmt.Sprintf("error rate %.3f above maximum %.3f", s.ErrorRate, g.MaxErrorRate))
	}
	if g.MaxLatencyP95Ms > 0 && s.LatencyP95Ms > 0 && s.LatencyP95Ms > g.MaxLatencyP95Ms {
		ev.Pass = false
		ev.Reasons = append(ev.Reasons,
			fmt.Sprintf("p95 latency %.0fms above maximum %.0fms", s.LatencyP95Ms, g.MaxLatencyP95Ms))
	}
	return ev
}
