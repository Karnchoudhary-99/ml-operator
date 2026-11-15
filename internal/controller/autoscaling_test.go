package controller

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

func autoscaleDep(name string, gen int64, replicas, min, max, cpu int32) api.ModelDeployment {
	d := dep(name, gen)
	d.Spec.Replicas = &replicas
	d.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: min, MaxReplicas: max, TargetCPUUtilization: cpu}
	return d
}

func hasEvent(rec *Reconciler, key, reason string) bool {
	for _, e := range rec.Events.EventsFor(key) {
		if e.Reason == reason {
			return true
		}
	}
	return false
}

func TestReconcileAutoscalingScalesUp(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(autoscaleDep("iris-mlp", 1, 2, 1, 5, 70))

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 4 {
		t.Fatalf("actions = %v, want 4 (create deployment + service + HPA + PDB)", res.Actions)
	}
	if res.Actions[2].Kind != ActionCreateHPA {
		t.Fatalf("third action = %v, want CreateHPA", res.Actions[2].Kind)
	}
	if cluster.HPACount() != 1 {
		t.Fatalf("HPA count = %d, want 1", cluster.HPACount())
	}
	if hpa, ok := cluster.GetHPA("default/iris-mlp"); !ok || hpa.MaxReplicas != 5 || hpa.TargetCPUUtilization != 70 {
		t.Fatalf("HPA = %+v ok=%v, want max 5 cpu 70", hpa, ok)
	}

	// A load spike (140% CPU vs 70% target) doubles the replicas.
	cluster.SetReady("default/iris-mlp", 2)
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 140, RequestsPerSecond: -1, ErrorRate: -1})
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	if live.Replicas != 4 {
		t.Fatalf("replicas after spike = %d, want 4", live.Replicas)
	}
	// A scale-up moves the Deployment and the PDB together
	// (minAvailable is replicas-1 by default, so 2 -> 4 replicas
	// moves it 1 -> 3).
	if len(res.Actions) != 2 || res.Actions[0].Kind != ActionUpdateDeployment || res.Actions[1].Kind != ActionUpdatePDB {
		t.Fatalf("actions = %v, want [UpdateDeployment UpdatePDB]", res.Actions)
	}
	if got := rec.Metrics.Counter(metrics.MetricScaleEventsTotal, "direction", "up"); got != 1 {
		t.Fatalf("scale-up counter = %v, want 1", got)
	}
	if !hasEvent(rec, "default/iris-mlp", "ScaledUp") {
		t.Fatal("no ScaledUp event recorded")
	}
	if got := rec.Metrics.Gauge(metrics.MetricDesiredReplicas, "deployment", "default/iris-mlp"); got != 4 {
		t.Fatalf("desired gauge = %v, want 4", got)
	}
}

func TestReconcileAutoscalingHoldsWithoutMetrics(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(autoscaleDep("iris-mlp", 1, 2, 1, 5, 70))

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	// No metrics observation: the scaler holds the current count.
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	if live.Replicas != 2 {
		t.Fatalf("replicas without metrics = %d, want 2 (held)", live.Replicas)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp", res.Actions)
	}
	if got := rec.Metrics.Counter(metrics.MetricScaleEventsTotal, "direction", "up"); got != 0 {
		t.Fatalf("scale counter = %v, want 0", got)
	}
	// The HPA object still exists: the cluster HPA and the
	// in-process scaler agree on the spec either way.
	if cluster.HPACount() != 1 {
		t.Fatalf("HPA count = %d, want 1", cluster.HPACount())
	}
}

func TestReconcileAutoscalingScaleDownStabilization(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Now()
	rec.Now = func() time.Time { return now }
	reg.Register(autoscaleDep("iris-mlp", 1, 4, 1, 10, 70))

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 4)

	// Load halves: first scale-down applies at once.
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 20, RequestsPerSecond: -1, ErrorRate: -1})
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	if live.Replicas != 2 {
		t.Fatalf("replicas after first dip = %d, want 2", live.Replicas)
	}
	if got := rec.Metrics.Counter(metrics.MetricScaleEventsTotal, "direction", "down"); got != 1 {
		t.Fatalf("scale-down counter = %v, want 1", got)
	}

	// One minute later the load dips further — held by the window.
	now = now.Add(time.Minute)
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 10, RequestsPerSecond: -1, ErrorRate: -1})
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	live, _ = cluster.GetDeployment("default/iris-mlp")
	if live.Replicas != 2 {
		t.Fatalf("replicas inside window = %d, want 2 (held)", live.Replicas)
	}
	if !hasEvent(rec, "default/iris-mlp", "ScaleDownHeld") {
		t.Fatal("no ScaleDownHeld event recorded")
	}
	if got := rec.Metrics.Counter(metrics.MetricScaleEventsTotal, "direction", "held"); got != 1 {
		t.Fatalf("held counter = %v, want 1", got)
	}

	// After the window, the same dip scales down.
	now = now.Add(6 * time.Minute)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	live, _ = cluster.GetDeployment("default/iris-mlp")
	if live.Replicas != 1 {
		t.Fatalf("replicas after window = %d, want 1", live.Replicas)
	}
}

func TestReconcileAutoscalingDisabledRemovesHPA(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(autoscaleDep("iris-mlp", 1, 2, 1, 5, 70))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if cluster.HPACount() != 1 {
		t.Fatalf("HPA count = %d, want 1", cluster.HPACount())
	}

	// Autoscaling off: the leftover HPA is garbage-collected.
	d := dep("iris-mlp", 2)
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range res.Actions {
		if a.Kind == ActionDeleteHPA {
			found = true
		}
	}
	if !found {
		t.Fatalf("actions = %v, want a DeleteHPA", res.Actions)
	}
	if cluster.HPACount() != 0 {
		t.Fatalf("HPA count = %d, want 0 after disabling autoscaling", cluster.HPACount())
	}
}

func TestReconcileCanaryGateUsesErrorRate(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// 4 replicas, canary 50% -> 2 canary replicas, fully ready, but
	// erroring half its requests: the gate must fail on error rate,
	// not readiness.
	d := canaryDep(2, 50, 2, 0)
	replicas := int32(4)
	d.Spec.Replicas = &replicas
	v1 := dep("iris-mlp", 1)
	v1.Spec.Replicas = &replicas
	reg.Register(v1)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 4)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	reg.Register(d)

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp-canary", 2)
	cluster.SetMetrics("default/iris-mlp-canary", ClusterMetrics{
		CPUUtilizationPercent: -1, RequestsPerSecond: -1, ErrorRate: 0.5,
		ModelLoadKnown: true, ModelLoaded: true,
	})
	// Three consecutive error-rate failures abort the rollout.
	for i := 0; i < 3; i++ {
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && (res.Phase != api.PhaseProgressing || !res.Requeue) {
			t.Fatalf("failure %d: phase=%s requeue=%v, want Progressing/true", i+1, res.Phase, res.Requeue)
		}
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
		t.Fatal("canary was not deleted after error-rate aborts")
	}
	if got := rec.Metrics.Counter(metrics.MetricRolloutsTotal, "result", "aborted"); got != 1 {
		t.Fatalf("aborted rollouts = %v, want 1", got)
	}
	if !hasEvent(rec, "default/iris-mlp", "RolloutAborted") {
		t.Fatal("no RolloutAborted event recorded")
	}
}

func TestReconcileCanaryModelLoadProbeHolds(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// Controllable clock: ApplyDefaults turns a 0 canary interval
	// into 30s, so the rollout only advances after the interval.
	now := time.Now()
	rec.Now = func() time.Time { return now }

	reg.Register(canaryDep(2, 10, 2, 0))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp-canary", 1)
	// Pods ready, model still loading: the gate holds (pending),
	// and the rollout must not burn failure budget or advance.
	cluster.SetMetrics("default/iris-mlp-canary", ClusterMetrics{
		CPUUtilizationPercent: -1, RequestsPerSecond: -1, ErrorRate: -1,
		ModelLoadKnown: true, ModelLoaded: false,
	})
	for i := 0; i < 3; i++ {
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != api.PhaseProgressing || !res.Requeue {
			t.Fatalf("probe hold %d: phase=%s requeue=%v, want Progressing/true", i+1, res.Phase, res.Requeue)
		}
		if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); !ok {
			t.Fatalf("probe hold %d: canary deleted while the model was loading", i+1)
		}
	}

	// Model loaded, interval elapsed: the gate passes and the
	// rollout advances.
	cluster.SetMetrics("default/iris-mlp-canary", ClusterMetrics{
		CPUUtilizationPercent: -1, RequestsPerSecond: -1, ErrorRate: -1,
		ModelLoadKnown: true, ModelLoaded: true,
	})
	now = now.Add(31 * time.Second)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range res.Actions {
		if a.Kind == ActionUpdateDeployment && a.Key == "default/iris-mlp-canary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("actions = %v, want a canary update (weight step after the model loaded)", res.Actions)
	}
}

func TestReconcileRecordsMetricsAndEvents(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics.Counter(metrics.MetricReconcilesTotal, "result", "available"); got != 1 {
		t.Fatalf("available reconciles = %v, want 1", got)
	}
	if got := rec.Metrics.Counter(metrics.MetricReconcilesTotal, "result", "progressing"); got != 1 {
		t.Fatalf("progressing reconciles = %v, want 1", got)
	}
	if got := rec.Metrics.Gauge(metrics.MetricReadyReplicas, "deployment", "default/iris-mlp"); got != 2 {
		t.Fatalf("ready gauge = %v, want 2", got)
	}
	// An invalid spec records a Warning event and a failed counter.
	bad := dep("bad name!", 2)
	reg.Register(bad)
	if _, err := rec.Reconcile("default/bad name!"); err != nil {
		t.Fatal(err)
	}
	if !hasEvent(rec, "default/bad name!", "InvalidSpec") {
		t.Fatal("no InvalidSpec event for the bad deployment")
	}
	if got := rec.Metrics.Counter(metrics.MetricReconcilesTotal, "result", "failed"); got != 1 {
		t.Fatalf("failed reconciles = %v, want 1", got)
	}
}
