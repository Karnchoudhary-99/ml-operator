package rollout

import (
	"reflect"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func TestStepWeights(t *testing.T) {
	cases := []struct {
		name string
		spec api.CanarySpec
		want []int32
	}{
		{"three steps from 10", api.CanarySpec{Weight: 10, Steps: 3}, []int32{10, 40, 70, 100}},
		{"four steps from 25", api.CanarySpec{Weight: 25, Steps: 4}, []int32{25, 43, 61, 79, 100}},
		{"zero steps is one jump", api.CanarySpec{Weight: 50, Steps: 0}, []int32{50, 100}},
		{"one step from 90", api.CanarySpec{Weight: 90, Steps: 1}, []int32{90, 100}},
		{"weight clamped below 1", api.CanarySpec{Weight: 0, Steps: 2}, []int32{1, 50, 100}},
		{"weight clamped above 99", api.CanarySpec{Weight: 120, Steps: 1}, []int32{99, 100}},
		{"negative steps treated as zero", api.CanarySpec{Weight: 20, Steps: -2}, []int32{20, 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StepWeights(tc.spec); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("StepWeights(%+v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestCanaryReplicas(t *testing.T) {
	cases := []struct {
		total, weight, want int32
	}{
		{4, 10, 1},    // rounds to nearest, minimum 1
		{4, 25, 1},    // 25% of 4 is exactly 1
		{4, 50, 2},    // 50% of 4 is 2
		{10, 25, 3},   // 2.5 rounds up to 3
		{10, 100, 10}, // full weight is the whole replica count
		{10, 0, 0},    // zero weight carries no traffic
		{0, 50, 0},    // no replicas to split
		{3, 99, 3},    // capped at the total, never above it
	}
	for _, tc := range cases {
		if got := CanaryReplicas(tc.total, tc.weight); got != tc.want {
			t.Fatalf("CanaryReplicas(%d, %d) = %d, want %d", tc.total, tc.weight, got, tc.want)
		}
	}
}
