package autoscale

import (
	"fmt"
	"math"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// DefaultScaleDownStabilization is how long a scale-down
// recommendation must wait before it applies — the HPA default of
// 5 minutes. A short load dip never tears pods down.
const DefaultScaleDownStabilization = 5 * time.Minute

// Sample is one load observation for a deployment: the average CPU
// utilization across its pods (percent, 0-100+) and the per-pod
// request rate. A negative value means "no observation" — the
// target is skipped rather than treated as zero load.
type Sample struct {
	// CPUUtilizationPercent is the average CPU utilization across
	// pods (percent of requested CPU). -1 means unknown.
	CPUUtilizationPercent float64
	// RequestsPerSecond is the average request rate per pod. -1
	// means unknown.
	RequestsPerSecond float64
}

// NoSample is a Sample with no observations at all.
var NoSample = Sample{CPUUtilizationPercent: -1, RequestsPerSecond: -1}

// Decision is one scaling verdict.
type Decision struct {
	// Desired is the replica count the deployment should run.
	Desired int32
	// Changed is true when Desired differs from the current count.
	Changed bool
	// Reason explains the decision in one line, e.g. `cpu 92% vs
	// target 70% -> 3 replicas (was 2)`.
	Reason string
	// Held is true when a scale-down was recommended but the
	// stabilization window is still open — the deployment keeps
	// its current replica count for now.
	Held bool
}

// DesiredReplicas is the stateless core: given the spec, the
// current replica count, and one load sample, it returns the
// replica count the deployment should run. Each enabled target
// proposes ceil(current * observed/target); the largest proposal
// wins; the result is clamped to [minReplicas, maxReplicas]
// (min defaults to 1 when unset). With no usable observation the
// current count is returned unchanged — unknown load never scales.
// Callers must run api.ApplyDefaults + api.Validate first.
func DesiredReplicas(spec api.AutoscalingSpec, current int32, s Sample) (int32, string) {
	if current < 1 {
		current = 1
	}
	min := spec.MinReplicas
	if min < 1 {
		min = 1
	}
	max := spec.MaxReplicas
	if max < min {
		max = min
	}

	desired := int32(0)
	var drivers []string
	if spec.TargetCPUUtilization > 0 && s.CPUUtilizationPercent >= 0 {
		proposed := int32(math.Ceil(float64(current) * s.CPUUtilizationPercent / float64(spec.TargetCPUUtilization)))
		if proposed > desired {
			desired = proposed
		}
		drivers = append(drivers, fmt.Sprintf("cpu %.0f%% vs target %d%%", s.CPUUtilizationPercent, spec.TargetCPUUtilization))
	}
	if spec.TargetRequestsPerSecond > 0 && s.RequestsPerSecond >= 0 {
		proposed := int32(math.Ceil(float64(current) * s.RequestsPerSecond / float64(spec.TargetRequestsPerSecond)))
		if proposed > desired {
			desired = proposed
		}
		drivers = append(drivers, fmt.Sprintf("rps %.1f vs target %d", s.RequestsPerSecond, spec.TargetRequestsPerSecond))
	}

	if len(drivers) == 0 {
		return clamp(current, min, max), "no load observation — keeping current replicas"
	}
	desired = clamp(desired, min, max)
	return desired, fmt.Sprintf("%s -> %d replicas (was %d)", joinDrivers(drivers), desired, current)
}

// Scaler is the stateful scaler for one deployment: it applies
// DesiredReplicas and holds scale-downs inside the stabilization
// window. One Scaler serves one deployment — the controller keeps a
// map keyed by "namespace/name". The caller passes the clock, so
// tests drive it deterministically.
type Scaler struct {
	// Stabilization is the scale-down hold window; 0 means the
	// default (5 minutes).
	Stabilization time.Duration

	lastScaleDown time.Time
	hasScaledDown bool
}

// Scale decides the replica count for now, given the spec, the
// current count, and the latest load sample. Scale-up applies
// immediately; a scale-down inside the stabilization window since
// the last scale-down is held (Decision.Held, Desired == current).
// A scale-down that applies records the time, starting a new window.
func (s *Scaler) Scale(spec api.AutoscalingSpec, current int32, sample Sample, now time.Time) Decision {
	desired, reason := DesiredReplicas(spec, current, sample)
	if desired >= current {
		return Decision{Desired: desired, Changed: desired != current, Reason: reason}
	}
	window := s.Stabilization
	if window <= 0 {
		window = DefaultScaleDownStabilization
	}
	if s.hasScaledDown && now.Sub(s.lastScaleDown) < window {
		return Decision{
			Desired: current,
			Changed: false,
			Reason:  fmt.Sprintf("scale-down to %d held: stabilization window (%s) still open", desired, window),
			Held:    true,
		}
	}
	s.lastScaleDown = now
	s.hasScaledDown = true
	return Decision{Desired: desired, Changed: true, Reason: reason}
}

func clamp(v, min, max int32) int32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func joinDrivers(drivers []string) string {
	out := ""
	for i, d := range drivers {
		if i > 0 {
			out += ", "
		}
		out += d
	}
	return out
}
