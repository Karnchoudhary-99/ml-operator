package controller

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

// blueGreenDep builds a v2 ModelDeployment with a BlueGreen
// strategy for the rollout tests.
func blueGreenDep(gen int64, gate *api.GateSpec) api.ModelDeployment {
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
			Strategy:  api.StrategyBlueGreen,
			BlueGreen: &api.BlueGreenSpec{Gate: gate},
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

func TestBlueGreenFirstDeployCreatesNoGreen(t *testing.T) {
	reg, cluster, rec := setup(t)
	// A first deployment has no live stable version to protect:
	// the new image applies directly, no green Deployment soaks.
	d := blueGreenDep(1, nil)
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v1"
	d.Spec.Model.Version = "v1"
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("first deploy created a green Deployment it does not need")
	}
	cluster.SetReady("default/iris-mlp", 2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase=%s, want Available", res.Phase)
	}
}

func TestBlueGreenFullLifecycle(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	reg.Register(blueGreenDep(2, nil))

	// Reconcile 1: stable stays on v1; only the green Deployment
	// (full size, 2 replicas) is created. The gate is pending (0
	// ready), so the rollout holds and requeues.
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionCreateDeployment || res.Actions[0].Key != "default/iris-mlp-green" {
		t.Fatalf("actions = %v, want a single green create", res.Actions)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 (pinned during rollout)", got.Container.Image)
	}
	green, _ := cluster.GetDeployment("default/iris-mlp-green")
	if green.Replicas != 2 {
		t.Fatalf("green replicas = %d, want 2 (full size)", green.Replicas)
	}
	if green.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("green image = %s, want v2", green.Container.Image)
	}
	// The Service still selects blue pods only: no traffic to green.
	svc, _ := cluster.GetService("default/iris-mlp")
	if svc.Selector["ml-operator/green"] != "false" {
		t.Fatalf("service selector = %v, want green=false while soaking", svc.Selector)
	}
	if res.Phase != api.PhaseProgressing || !res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Progressing/true", res.Phase, res.Requeue)
	}

	// Green pods come up: the gate passes, the Service flips to
	// green. Traffic never splits — it moves all at once.
	cluster.SetReady("default/iris-mlp-green", 2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionUpdateService || res.Actions[0].Key != "default/iris-mlp" {
		t.Fatalf("actions = %v, want a single service update (switch)", res.Actions)
	}
	svc, _ = cluster.GetService("default/iris-mlp")
	if svc.Selector["ml-operator/green"] != "true" {
		t.Fatalf("service selector = %v, want green=true after the switch", svc.Selector)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 until the fold", got.Container.Image)
	}
	if res.Phase != api.PhaseProgressing || !res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Progressing/true", res.Phase, res.Requeue)
	}

	// Next reconcile folds: stable takes v2, the Service flips back
	// to blue (which is now v2), green is deleted.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("stable image = %s, want v2 after the fold", got.Container.Image)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment was not deleted on fold")
	}
	svc, _ = cluster.GetService("default/iris-mlp")
	if svc.Selector["ml-operator/green"] != "false" {
		t.Fatalf("service selector = %v, want green=false after the fold", svc.Selector)
	}
	// The replaced image is the rollback target.
	if st := reg.Get("default/iris-mlp").Deployment.Status; st.PreviousImage != "registry.example.com/iris-mlp:v1" || st.PreviousVersion != "v1" {
		t.Fatalf("rollback target wrong: %+v", st)
	}
	if res.Phase != api.PhaseAvailable || res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Available/false", res.Phase, res.Requeue)
	}

	// Reconciling the same generation again must not restart the
	// rollout: no green comes back.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp (rollout done)", res.Actions)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment came back after the fold")
	}
}

func TestBlueGreenAbortsOnGateFailures(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)
	reg.Register(blueGreenDep(2, nil))

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	// Green is fully ready but its error rate is 50%: a real gate
	// failure, three reconciles in a row.
	cluster.SetReady("default/iris-mlp-green", 2)
	cluster.SetMetrics("default/iris-mlp-green", ClusterMetrics{ErrorRate: 0.5})
	for i := 0; i < 2; i++ {
		res, err := rec.Reconcile("default/iris-mlp")
		if err != nil {
			t.Fatal(err)
		}
		if res.Phase != api.PhaseProgressing || !res.Requeue {
			t.Fatalf("failure %d: phase=%s requeue=%v, want Progressing/true", i+1, res.Phase, res.Requeue)
		}
		if _, ok := cluster.GetDeployment("default/iris-mlp-green"); !ok {
			t.Fatalf("failure %d: green deleted too early", i+1)
		}
	}
	// Third consecutive failure aborts: green deleted, Degraded,
	// no requeue — terminal for this generation.
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != api.PhaseDegraded || res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Degraded/false", res.Phase, res.Requeue)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment was not deleted on abort")
	}
	// The Service never flipped: it still selects blue pods.
	svc, _ := cluster.GetService("default/iris-mlp")
	if svc.Selector["ml-operator/green"] != "false" {
		t.Fatalf("service selector = %v, want green=false after abort", svc.Selector)
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
	if res.Phase != api.PhaseDegraded {
		t.Fatalf("phase=%s after abort, want Degraded", res.Phase)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment came back after abort")
	}
}

func TestBlueGreenGateThresholds(t *testing.T) {
	// The same 10% error rate switches under a lenient 20% gate
	// and aborts under a strict 1% gate: thresholds tune the gate,
	// they never disable it.
	for _, tc := range []struct {
		name       string
		maxErr     float64
		wantSwitch bool
	}{
		{"lenient gate switches", 0.20, true},
		{"strict gate aborts", 0.01, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, cluster, rec := setup(t)
			availableV1(t, reg, cluster, rec)
			reg.Register(blueGreenDep(2, &api.GateSpec{MaxErrorRate: tc.maxErr}))
			if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
				t.Fatal(err)
			}
			cluster.SetReady("default/iris-mlp-green", 2)
			cluster.SetMetrics("default/iris-mlp-green", ClusterMetrics{ErrorRate: 0.10})
			if tc.wantSwitch {
				res, err := rec.Reconcile("default/iris-mlp")
				if err != nil {
					t.Fatal(err)
				}
				svc, _ := cluster.GetService("default/iris-mlp")
				if svc.Selector["ml-operator/green"] != "true" {
					t.Fatalf("service selector = %v, want green=true (switched)", svc.Selector)
				}
				if res.Phase != api.PhaseProgressing {
					t.Fatalf("phase=%s, want Progressing", res.Phase)
				}
				return
			}
			for i := 0; i < 3; i++ {
				if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
				t.Fatal("green Deployment was not deleted after three gate failures")
			}
			if st := reg.Get("default/iris-mlp").Deployment.Status; st.Phase != api.PhaseDegraded {
				t.Fatalf("phase=%s, want Degraded", st.Phase)
			}
		})
	}
}

func TestBlueGreenPlan(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)
	reg.Register(blueGreenDep(2, nil))

	// Registering a new generation resets the status; capture it so
	// the Plan's no-side-effect claim is judged against the state
	// Plan actually saw.
	before := reg.Get("default/iris-mlp").Deployment.Status

	// The plan previews the green create and pins the stable image:
	// no stable update, no service change.
	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != ActionCreateDeployment || actions[0].Key != "default/iris-mlp-green" {
		t.Fatalf("plan = %v, want a single green create", actions)
	}
	// A plan changes nothing: no green in the cluster, no rollout
	// state stored, no status written.
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("Plan created a green Deployment")
	}
	if _, ok := rec.BlueGreenState("default/iris-mlp"); ok {
		t.Fatal("Plan stored blue-green rollout state")
	}
	if st := reg.Get("default/iris-mlp").Deployment.Status; st.Phase != before.Phase || st.ReadyReplicas != before.ReadyReplicas || st.ObservedGeneration != before.ObservedGeneration || st.Message != before.Message {
		t.Fatalf("Plan changed the status: %+v (was %+v)", st, before)
	}
}

func TestBlueGreenNoImageChangeSkipsGreen(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// A replica-only change under BlueGreen is not a version change:
	// no green Deployment soaks, the stable Deployment updates
	// directly.
	d := dep("iris-mlp", 2)
	replicas := int32(3)
	d.Spec.Replicas = &replicas
	d.Spec.Strategy = api.StrategyBlueGreen
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("replica-only change created a green Deployment")
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Replicas != 3 {
		t.Fatalf("stable replicas = %d, want 3", got.Replicas)
	}
	if res.Phase != api.PhaseDegraded {
		t.Fatalf("phase=%s, want Degraded (2 of 3 replicas ready — serving below the desired count)", res.Phase)
	}
}

func TestBlueGreenDeleteCleansGreen(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)
	reg.Register(blueGreenDep(2, nil))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); !ok {
		t.Fatal("setup: no green Deployment to clean up")
	}

	reg.Delete("default/iris-mlp")
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment survived the delete")
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp"); ok {
		t.Fatal("stable Deployment survived the delete")
	}
	found := false
	for _, a := range res.Actions {
		if a.Key == "default/iris-mlp-green" && a.Kind == ActionDeleteDeployment {
			found = true
		}
	}
	if !found {
		t.Fatalf("actions = %v, want a green delete", res.Actions)
	}
}

// TestBlueGreenStateSurvivesRestore checks the store round-trip
// helpers: a stored state restores only for the same generation and
// strategy, and a stale one is dropped.
func TestBlueGreenStateRestore(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)
	reg.Register(blueGreenDep(2, nil))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	states := rec.BlueGreenStates()
	s, ok := states["default/iris-mlp"]
	if !ok || s.Generation != 2 {
		t.Fatalf("BlueGreenStates = %v, want generation 2", states)
	}

	// A fresh reconciler restores the same-generation state.
	reg2 := registry.New()
	reg2.Register(blueGreenDep(2, nil))
	rec2 := New(reg2, NewFakeCluster())
	rec2.RestoreBlueGreenState("default/iris-mlp", s)
	if got, ok := rec2.BlueGreenState("default/iris-mlp"); !ok || got.Generation != 2 {
		t.Fatalf("restored state = %+v/%v, want generation 2", got, ok)
	}
	// A new generation drops it: restoring a generation-2 state
	// onto a generation-3 deployment stores nothing.
	reg3 := registry.New()
	reg3.Register(blueGreenDep(3, nil))
	rec3 := New(reg3, NewFakeCluster())
	rec3.RestoreBlueGreenState("default/iris-mlp", s)
	if _, ok := rec3.BlueGreenState("default/iris-mlp"); ok {
		t.Fatal("stale (generation 2) state restored for generation 3")
	}
}
