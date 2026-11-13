package autoscale

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func spec(min, max, cpu, rps int32) api.AutoscalingSpec {
	return api.AutoscalingSpec{MinReplicas: min, MaxReplicas: max, TargetCPUUtilization: cpu, TargetRequestsPerSecond: rps}
}

func TestDesiredReplicasCPU(t *testing.T) {
	cases := []struct {
		name    string
		spec    api.AutoscalingSpec
		current int32
		sample  Sample
		want    int32
	}{
		{"cpu over target scales up", spec(1, 10, 70, 0), 2, Sample{CPUUtilizationPercent: 140, RequestsPerSecond: -1}, 4},
		{"cpu under target scales down to min", spec(1, 10, 70, 0), 4, Sample{CPUUtilizationPercent: 20, RequestsPerSecond: -1}, 2},
		{"cpu at target holds", spec(1, 10, 70, 0), 3, Sample{CPUUtilizationPercent: 70, RequestsPerSecond: -1}, 3},
		{"cpu rounds up a fraction", spec(1, 10, 70, 0), 2, Sample{CPUUtilizationPercent: 80, RequestsPerSecond: -1}, 3},
		{"cpu clamped at max", spec(1, 3, 50, 0), 2, Sample{CPUUtilizationPercent: 200, RequestsPerSecond: -1}, 3},
		{"cpu clamped at min", spec(2, 10, 70, 0), 3, Sample{CPUUtilizationPercent: 5, RequestsPerSecond: -1}, 2},
		{"min 0 defaults to 1", spec(0, 5, 70, 0), 1, Sample{CPUUtilizationPercent: 10, RequestsPerSecond: -1}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := DesiredReplicas(tc.spec, tc.current, tc.sample)
			if got != tc.want {
				t.Fatalf("DesiredReplicas = %d, want %d (reason %q)", got, tc.want, reason)
			}
			if reason == "" {
				t.Fatal("reason must not be empty")
			}
		})
	}
}

func TestDesiredReplicasRPS(t *testing.T) {
	s := spec(1, 10, 0, 100)
	got, _ := DesiredReplicas(s, 2, Sample{CPUUtilizationPercent: -1, RequestsPerSecond: 250})
	if got != 5 {
		t.Fatalf("rps 250 vs target 100 from 2 pods = %d, want 5", got)
	}
	// RPS proposal wins over a lower CPU proposal.
	s = spec(1, 10, 70, 100)
	got, _ = DesiredReplicas(s, 2, Sample{CPUUtilizationPercent: 70, RequestsPerSecond: 300})
	if got != 6 {
		t.Fatalf("max(cpu 2, rps 6) = %d, want 6", got)
	}
}

func TestDesiredReplicasNoObservation(t *testing.T) {
	got, reason := DesiredReplicas(spec(1, 10, 70, 100), 3, NoSample)
	if got != 3 {
		t.Fatalf("no observation must keep current: got %d, want 3", got)
	}
	if reason == "" {
		t.Fatal("reason must not be empty")
	}
	// A sample with only an unknown CPU and a known RPS still scales on RPS.
	got, _ = DesiredReplicas(spec(1, 10, 70, 100), 2, Sample{CPUUtilizationPercent: -1, RequestsPerSecond: 150})
	if got != 3 {
		t.Fatalf("rps-only sample = %d, want 3", got)
	}
}

func TestScalerScaleDownStabilization(t *testing.T) {
	sc := &Scaler{}
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	s := spec(1, 10, 70, 0)

	// First scale-down applies immediately (no window open yet).
	d := sc.Scale(s, 4, Sample{CPUUtilizationPercent: 20, RequestsPerSecond: -1}, now)
	if !d.Changed || d.Desired != 2 || d.Held {
		t.Fatalf("first scale-down = %+v, want changed to 2, not held", d)
	}
	// One minute later the load dips again — held inside the window.
	d = sc.Scale(s, 2, Sample{CPUUtilizationPercent: 10, RequestsPerSecond: -1}, now.Add(time.Minute))
	if d.Held != true || d.Desired != 2 || d.Changed {
		t.Fatalf("scale-down inside window = %+v, want held at 2", d)
	}
	// After the window passes, a scale-down applies again.
	d = sc.Scale(s, 2, Sample{CPUUtilizationPercent: 10, RequestsPerSecond: -1}, now.Add(6*time.Minute))
	if d.Held || !d.Changed || d.Desired != 1 {
		t.Fatalf("scale-down after window = %+v, want changed to 1", d)
	}
}

func TestScalerScaleUpImmediate(t *testing.T) {
	sc := &Scaler{}
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	s := spec(1, 10, 70, 0)
	// Even right after a scale-down, a spike scales up at once.
	sc.Scale(s, 4, Sample{CPUUtilizationPercent: 20, RequestsPerSecond: -1}, now)
	d := sc.Scale(s, 2, Sample{CPUUtilizationPercent: 210, RequestsPerSecond: -1}, now.Add(time.Minute))
	if !d.Changed || d.Desired != 6 || d.Held {
		t.Fatalf("scale-up after scale-down = %+v, want changed to 6, not held", d)
	}
}

func TestScalerCustomStabilization(t *testing.T) {
	sc := &Scaler{Stabilization: time.Minute}
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	s := spec(1, 10, 70, 0)
	sc.Scale(s, 4, Sample{CPUUtilizationPercent: 20, RequestsPerSecond: -1}, now)
	d := sc.Scale(s, 2, Sample{CPUUtilizationPercent: 10, RequestsPerSecond: -1}, now.Add(90*time.Second))
	if d.Held || d.Desired != 1 {
		t.Fatalf("custom 1m window: scale-down at 90s = %+v, want applied to 1", d)
	}
}
