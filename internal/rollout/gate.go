package rollout

import "fmt"

// Gate is the health gate a canary step must pass before the rollout
// advances. Thresholds set to 0 are skipped, except MinReadyRatio,
// which defaults to 1.0 (every canary pod ready) — a canary that is
// not fully ready has not proven itself yet. The readiness check is
// always available (the cluster reports it); error rate and latency
// need a metrics source, so the controller only fills them when it
// has one — today that is never, and the gate is readiness-only.
// The metrics phase wires real error-rate/latency samples in.
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
