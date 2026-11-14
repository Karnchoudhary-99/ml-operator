package rollout

import (
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name           string
		gate           Gate
		sample         Sample
		wantPass       bool
		wantPending    bool
		wantReasonPart string
	}{
		{
			"all ready passes the default gate",
			Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2},
			true, false, "",
		},
		{
			"partial readiness fails the default all-ready gate",
			Gate{}, Sample{ReadyReplicas: 1, DesiredReplicas: 2},
			false, false, "ready ratio 0.50 below minimum 1.00",
		},
		{
			"partial readiness passes a relaxed ratio",
			Gate{MinReadyRatio: 0.5}, Sample{ReadyReplicas: 1, DesiredReplicas: 2},
			true, false, "",
		},
		{
			"zero ready is pending, not a failure",
			Gate{}, Sample{ReadyReplicas: 0, DesiredReplicas: 2},
			false, true, "pods still starting",
		},
		{
			"zero desired is pending",
			Gate{}, Sample{ReadyReplicas: 0, DesiredReplicas: 0},
			false, true, "no desired canary replicas",
		},
		{
			"error rate above the threshold fails",
			Gate{MaxErrorRate: 0.01}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ErrorRate: 0.05},
			false, false, "error rate 0.050 above maximum 0.010",
		},
		{
			"error rate below the threshold passes",
			Gate{MaxErrorRate: 0.01}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ErrorRate: 0.005},
			true, false, "",
		},
		{
			"no error threshold means no error check",
			Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ErrorRate: 0.9},
			true, false, "",
		},
		{
			"p95 latency above the threshold fails",
			Gate{MaxLatencyP95Ms: 200}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, LatencyP95Ms: 450},
			false, false, "p95 latency 450ms above maximum 200ms",
		},
		{
			"multiple failures are all reported",
			Gate{MinReadyRatio: 1.0, MaxErrorRate: 0.01, MaxLatencyP95Ms: 100},
			Sample{ReadyReplicas: 1, DesiredReplicas: 2, ErrorRate: 0.5, LatencyP95Ms: 500},
			false, false, "ready ratio",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.gate, tc.sample)
			if got.Pass != tc.wantPass {
				t.Fatalf("Pass = %v, want %v (reasons %v)", got.Pass, tc.wantPass, got.Reasons)
			}
			if got.Pending != tc.wantPending {
				t.Fatalf("Pending = %v, want %v", got.Pending, tc.wantPending)
			}
			if tc.wantReasonPart != "" {
				joined := strings.Join(got.Reasons, "; ")
				if !strings.Contains(joined, tc.wantReasonPart) {
					t.Fatalf("reasons %q do not contain %q", joined, tc.wantReasonPart)
				}
			}
			if tc.name == "multiple failures are all reported" && len(got.Reasons) != 3 {
				t.Fatalf("reasons = %v, want 3 (readiness, error rate, latency)", got.Reasons)
			}
		})
	}
}

func TestEvaluateModelLoadProbe(t *testing.T) {
	// Pods ready but the model not loaded: pending, never a failure.
	ev := Evaluate(Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ModelLoadKnown: true, ModelLoaded: false})
	if ev.Pass || !ev.Pending {
		t.Fatalf("model not loaded = %+v, want pending, not pass", ev)
	}
	if !strings.Contains(strings.Join(ev.Reasons, " "), "model not loaded yet") {
		t.Fatalf("reasons = %v, want model-load reason", ev.Reasons)
	}
	// Model loaded: the gate passes on readiness as before.
	ev = Evaluate(Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ModelLoadKnown: true, ModelLoaded: true})
	if !ev.Pass || ev.Pending {
		t.Fatalf("model loaded = %+v, want pass", ev)
	}
	// Probe has not reported yet: skipped, gate judges readiness only.
	ev = Evaluate(Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ModelLoadKnown: false, ModelLoaded: false})
	if !ev.Pass {
		t.Fatalf("probe unknown = %+v, want pass (check skipped)", ev)
	}
	// Probe reported a failed load: a real failure, not a hold — it
	// burns failure budget so the rollout aborts instead of
	// waiting forever for a model that will never load.
	ev = Evaluate(Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ModelLoadKnown: true, ModelLoaded: false, ModelLoadFailed: true})
	if ev.Pass || ev.Pending {
		t.Fatalf("model load failed = %+v, want fail, not pass or pending", ev)
	}
	if !strings.Contains(strings.Join(ev.Reasons, " "), "model failed to load") {
		t.Fatalf("reasons = %v, want model-load-failure reason", ev.Reasons)
	}
	// Failed flag without a probe report means nothing: the probe
	// has not spoken, so the check is skipped like the others.
	ev = Evaluate(Gate{}, Sample{ReadyReplicas: 2, DesiredReplicas: 2, ModelLoadKnown: false, ModelLoaded: false, ModelLoadFailed: true})
	if !ev.Pass {
		t.Fatalf("failed flag without probe report = %+v, want pass (check skipped)", ev)
	}
}
