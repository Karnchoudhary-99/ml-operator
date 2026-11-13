package controller

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

// canaryDep builds a v2 ModelDeployment with a Canary strategy for
// the rollout tests. The ladder for Weight 10 / Steps 2 is
// [10 55 100]; interval 0 advances on every passing reconcile.
func canaryDep(gen int64, weight, steps, interval int32) api.ModelDeployment {
	replicas := int32(2)
	return api.ModelDeployment{
		Name:       "iris-mlp",
		Namespace:  "default",
		Generation: gen,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v2", Framework: "onnx", Version: "v2"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyCanary,
			Canary:    &api.CanarySpec{Weight: weight, Steps: steps, IntervalSeconds: interval},
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

// availableV1 registers v1 with a Rolling strategy and drives it to
// Available, so the canary tests start from a serving deployment.
func availableV1(t *testing.T, reg *registry.Registry, cluster *FakeCluster, rec *Reconciler) {
	t.Helper()
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("setup: phase=%s, want Available", res.Phase)
	}
}

func TestReconcileCanaryRolloutPromotes(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// Controllable clock: the rollout interval is 30s.
	now := time.Now()
	rec.Now = func() time.Time { return now }

	reg.Register(canaryDep(2, 10, 2, 30))

	// Reconcile 1: the stable Deployment stays on v1; only the
	// canary (10% -> 1 replica) is created. The gate is pending
	// (0 ready), so the rollout holds and requeues.
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionCreateDeployment || res.Actions[0].Key != "default/iris-mlp-canary" {
		t.Fatalf("actions = %v, want a single canary create", res.Actions)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 (pinned during rollout)", got.Container.Image)
	}
	if res.Phase != api.PhaseProgressing || !res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Progressing/true", res.Phase, res.Requeue)
	}

	// Canary pods come up, but the interval has not elapsed: hold.
	cluster.SetReady("default/iris-mlp-canary", 1)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp-canary"); got.Replicas != 1 {
		t.Fatalf("canary replicas = %d, want 1 (still at 10%%)", got.Replicas)
	}

	// 30s later the gate passes and the interval elapsed: advance
	// to 55% (still 1 of 2 replicas).
	now = now.Add(30 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionUpdateDeployment || res.Actions[0].Key != "default/iris-mlp-canary" {
		t.Fatalf("actions = %v, want a single canary update (weight step)", res.Actions)
	}

	// Another 30s: advance to 100% (2 replicas). The new canary pod
	// is not ready yet, so the next gate must see it ready first.
	now = now.Add(30 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	canary, _ := cluster.GetDeployment("default/iris-mlp-canary")
	if canary.Replicas != 2 {
		t.Fatalf("canary replicas = %d, want 2 (100%%)", canary.Replicas)
	}
	cluster.SetReady("default/iris-mlp-canary", 2)

	// Gate passes at the final weight: promote. The stable
	// Deployment takes v2, the canary is deleted, and the deployment
	// is Available again.
	now = now.Add(30 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("stable image = %s, want v2 after promotion", got.Container.Image)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
		t.Fatal("canary Deployment was not deleted on promotion")
	}
	if res.Phase != api.PhaseAvailable || res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Available/false", res.Phase, res.Requeue)
	}

	// Reconciling the same generation again must not restart the
	// rollout: no canary comes back.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp (rollout done)", res.Actions)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
		t.Fatal("canary Deployment came back after promotion")
	}
}

func TestReconcileCanaryRolloutAborts(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// 4 replicas, Weight 50 -> canary is 2 replicas, so 1 ready is
	// a real gate failure (ratio 0.5 < 1.0). Interval 0: the gate is
	// evaluated on every reconcile.
	d := canaryDep(2, 50, 2, 0)
	replicas := int32(4)
	d.Spec.Replicas = &replicas
	// v1 was serving 2 replicas; bump the stable count to 4 first
	// via a rolling update so the numbers line up.
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

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Key != "default/iris-mlp-canary" {
		t.Fatalf("actions = %v, want a single canary create", res.Actions)
	}

	// The canary never gets fully ready: 1 of 2, three reconciles.
	cluster.SetReady("default/iris-mlp-canary", 1)
	for i := 0; i < 2; i++ {
		res, err = rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != api.PhaseProgressing || !res.Requeue {
			t.Fatalf("failure %d: phase=%s requeue=%v, want Progressing/true", i+1, res.Phase, res.Requeue)
		}
		if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); !ok {
			t.Fatalf("failure %d: canary deleted too early", i+1)
		}
	}
	// Third consecutive failure aborts: canary deleted, Degraded,
	// no requeue — terminal for this generation.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != api.PhaseDegraded || res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Degraded/false", res.Phase, res.Requeue)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
		t.Fatal("canary Deployment was not deleted on abort")
	}
	// The stable Deployment still serves v1, and the next reconcile
	// does not restart the rollout or push v2 onto stable.
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 after abort", got.Container.Image)
	}
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp (aborted rollout stays aborted)", res.Actions)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s after re-reconcile, want v1", got.Container.Image)
	}
}

// TestReconcileCanaryGateUsesSpecThresholds proves the reconciler
// judges a canary by its own spec's gate, not the old hardcoded
// 5% / 500ms defaults: the same 10% error rate promotes under a
// lenient 20% gate, aborts under the default gate (nil gate), and
// aborts under a strict 1% gate.
func TestReconcileCanaryGateUsesSpecThresholds(t *testing.T) {
	// Drive one failing-gate reconcile; the caller sets readiness
	// and metrics first. Returns the reconcile result.
	drive := func(t *testing.T, rec *Reconciler) Result {
		t.Helper()
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Abort asserts the terminal aborted state after 3 failures.
	assertAborted := func(t *testing.T, cluster *FakeCluster, res Result) {
		t.Helper()
		if res.Phase != api.PhaseDegraded || res.Requeue {
			t.Fatalf("phase=%s requeue=%v, want Degraded/false", res.Phase, res.Requeue)
		}
		if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
			t.Fatal("canary Deployment was not deleted on abort")
		}
		if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
			t.Fatalf("stable image = %s, want v1 after abort", got.Container.Image)
		}
	}

	t.Run("lenient gate promotes", func(t *testing.T) {
		reg, cluster, rec := setup(t)
		availableV1(t, reg, cluster, rec)
		// Controllable clock: ApplyDefaults turns a 0 interval
		// into the 30s default, so steps wait out 30s.
		now := time.Now()
		rec.Now = func() time.Time { return now }
		// Weight 50 of 2 replicas -> 1 canary replica; steps 0
		// means one jump to 100% after the gate passes.
		d := canaryDep(2, 50, 0, 0)
		d.Spec.Canary.Gate = &api.GateSpec{MaxErrorRate: 0.20, MaxP95LatencyMs: 500, MinReadyRatio: 1.0}
		reg.Register(d)
		drive(t, rec) // creates the canary (pending: 0 ready)
		cluster.SetReady("default/iris-mlp-canary", 1)
		cluster.SetMetrics("default/iris-mlp-canary", ClusterMetrics{ErrorRate: 0.10})
		// 10% errors pass the lenient 20% gate, and the step
		// interval has elapsed: advance to 100%.
		now = now.Add(31 * time.Second)
		res := drive(t, rec)
		if res.Phase != api.PhaseProgressing || !res.Requeue {
			t.Fatalf("advance: phase=%s requeue=%v, want Progressing/true", res.Phase, res.Requeue)
		}
		// The canary grew to 2 replicas at 100%; bring both up.
		cluster.SetReady("default/iris-mlp-canary", 2)
		cluster.SetReady("default/iris-mlp", 2)
		res = drive(t, rec)
		if res.Phase != api.PhaseAvailable {
			t.Fatalf("promote: phase=%s, want Available", res.Phase)
		}
		if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
			t.Fatalf("stable image = %s, want v2 after promote", got.Container.Image)
		}
	})

	for _, tc := range []struct {
		name string
		gate *api.GateSpec // nil means "no gate in the spec"
	}{
		{"default gate aborts", nil},
		{"strict gate aborts", &api.GateSpec{MaxErrorRate: 0.01, MaxP95LatencyMs: 500, MinReadyRatio: 1.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, cluster, rec := setup(t)
			availableV1(t, reg, cluster, rec)
			d := canaryDep(2, 50, 0, 0)
			d.Spec.Canary.Gate = tc.gate
			reg.Register(d)
			drive(t, rec) // creates the canary (pending: 0 ready)
			cluster.SetReady("default/iris-mlp-canary", 1)
			cluster.SetMetrics("default/iris-mlp-canary", ClusterMetrics{ErrorRate: 0.10})
			// Three consecutive gate failures abort: 10% errors
			// beat both the 5% default and the 1% strict gate.
			var res Result
			for i := 0; i < 3; i++ {
				res = drive(t, rec)
			}
			assertAborted(t, cluster, res)
		})
	}
}

// TestReconcileRecordsPreviousImage proves the reconciler remembers
// the replaced image in status — on a canary promotion and on a
// direct rolling update — and keeps it across later reconciles that
// change nothing.
func TestReconcileRecordsPreviousImage(t *testing.T) {
	previous := func(t *testing.T, reg *registry.Registry) (string, string) {
		t.Helper()
		e := reg.Get("default/iris-mlp")
		if e == nil {
			t.Fatal("deployment left the registry")
		}
		return e.Deployment.Status.PreviousImage, e.Deployment.Status.PreviousVersion
	}

	t.Run("promote records previous", func(t *testing.T) {
		reg, cluster, rec := setup(t)
		availableV1(t, reg, cluster, rec)
		now := time.Now()
		rec.Now = func() time.Time { return now }
		reg.Register(canaryDep(2, 10, 2, 30))
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		cluster.SetReady("default/iris-mlp-canary", 1)
		now = now.Add(31 * time.Second)
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(31 * time.Second)
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		cluster.SetReady("default/iris-mlp-canary", 2)
		now = now.Add(31 * time.Second)
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != api.PhaseAvailable {
			t.Fatalf("promote: phase=%s, want Available", res.Phase)
		}
		if img, ver := previous(t, reg); img != "registry.example.com/iris-mlp:v1" || ver != "v1" {
			t.Fatalf("previous = %q (%q), want v1 image/version", img, ver)
		}
		// A later reconcile that changes nothing keeps it.
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		if img, ver := previous(t, reg); img != "registry.example.com/iris-mlp:v1" || ver != "v1" {
			t.Fatalf("previous after no-op reconcile = %q (%q), want v1", img, ver)
		}
	})

	t.Run("rolling update records previous", func(t *testing.T) {
		reg, cluster, rec := setup(t)
		availableV1(t, reg, cluster, rec)
		v2 := dep("iris-mlp", 2)
		v2.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
		v2.Spec.Model.Version = "v2"
		reg.Register(v2)
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		cluster.SetReady("default/iris-mlp", 2)
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != api.PhaseAvailable {
			t.Fatalf("phase=%s, want Available", res.Phase)
		}
		if img, ver := previous(t, reg); img != "registry.example.com/iris-mlp:v1" || ver != "v1" {
			t.Fatalf("previous = %q (%q), want v1 image/version", img, ver)
		}
	})

	t.Run("first creation records nothing", func(t *testing.T) {
		reg, _, rec := setup(t)
		reg.Register(dep("iris-mlp", 1))
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			t.Fatal(err)
		}
		if img, ver := previous(t, reg); img != "" || ver != "" {
			t.Fatalf("previous = %q (%q), want empty on first creation", img, ver)
		}
	})
}
