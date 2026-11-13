package rollout

import (
	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// StepWeights builds the traffic-weight ladder for a canary rollout
// from the spec: it starts at spec.Weight and climbs to 100 in
// spec.Steps increments. Steps == 0 means a single jump (Weight
// straight to 100% after the gate passes). The last entry is always
// exactly 100, so a rollout that reaches the end of the ladder is a
// full promotion. Intermediate entries are rounded down, which can
// make the final jump slightly larger than the others — the ladder
// is documented, not hidden, so operators can see it in status.
//
// Examples: Weight 10, Steps 3 -> [10 40 70 100]; Weight 25,
// Steps 4 -> [25 43 61 79 100]; Weight 50, Steps 0 -> [50 100].
func StepWeights(spec api.CanarySpec) []int32 {
	weight := spec.Weight
	if weight < 1 {
		weight = 1
	}
	if weight > 99 {
		weight = 99
	}
	steps := spec.Steps
	if steps < 0 {
		steps = 0
	}
	if steps == 0 {
		return []int32{weight, 100}
	}
	ladder := make([]int32, 0, steps+1)
	ladder = append(ladder, weight)
	// Even increments, rounded down; the final entry is forced to
	// 100 so the ladder always terminates in a full promotion.
	incr := (100 - weight) / steps
	for i := int32(1); i < steps; i++ {
		ladder = append(ladder, weight+incr*i)
	}
	ladder = append(ladder, 100)
	return ladder
}

// CanaryReplicas sizes the canary Deployment for a traffic weight:
// the weight percentage of the stable replica count, rounded to the
// nearest pod, minimum 1 while the weight is positive. Weight 0
// (rollout not started, or aborted) sizes to 0 — the canary carries
// no traffic. The rounding matches render.CanaryDeploymentFor so a
// weight and a replica count never disagree about each other.
func CanaryReplicas(totalReplicas, weight int32) int32 {
	if weight <= 0 || totalReplicas <= 0 {
		return 0
	}
	if weight >= 100 {
		return totalReplicas
	}
	replicas := (totalReplicas*weight + 50) / 100
	if replicas < 1 {
		replicas = 1
	}
	if replicas > totalReplicas {
		replicas = totalReplicas
	}
	return replicas
}
