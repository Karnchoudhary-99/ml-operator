package controller

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

func dep(name string, gen int64) api.ModelDeployment {
	replicas := int32(2)
	return api.ModelDeployment{
		Name:       name,
		Namespace:  "default",
		Generation: gen,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyRolling,
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

func setup(t testing.TB) (*registry.Registry, *FakeCluster, *Reconciler) {
	t.Helper()
	reg := registry.New()
	cluster := NewFakeCluster()
	return reg, cluster, New(reg, cluster)
}

func TestReconcileCreateThenAvailable(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseProgressing || !res.Requeue {
		t.Fatalf("after create: phase=%s requeue=%v, want Progressing/true", res.Phase, res.Requeue)
	}
	if len(res.Actions) != 3 { // CreateDeployment + CreateService + CreatePDB
		t.Fatalf("actions = %v, want 3 (create deployment + service + PDB)", res.Actions)
	}
	if res.Actions[0].Kind != ActionCreateDeployment || res.Actions[1].Kind != ActionCreateService {
		t.Fatalf("action order wrong: %v", res.Actions)
	}
	if cluster.DeploymentCount() != 1 || cluster.ServiceCount() != 1 {
		t.Fatalf("cluster holds %d deployments / %d services, want 1/1", cluster.DeploymentCount(), cluster.ServiceCount())
	}
	// Status recorded in the registry, with the generation observed.
	st := reg.Get("default/iris-mlp").Deployment.Status
	if st.Phase != api.PhaseProgressing || st.ObservedGeneration != 1 || st.ReadyReplicas != 0 {
		t.Fatalf("status wrong: %+v", st)
	}
	if c := st.ConditionFor("Ready"); c == nil || c.Status != "False" {
		t.Fatalf("Ready condition wrong: %+v", c)
	}

	// Second reconcile before pods are ready: nothing to apply.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("second reconcile applied %v, want a single NoOp", res.Actions)
	}

	// Pods come up: the next reconcile reports Available.
	cluster.SetReady("default/iris-mlp", 2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseAvailable || res.Requeue {
		t.Fatalf("after ready: phase=%s requeue=%v, want Available/false", res.Phase, res.Requeue)
	}
	if c := reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Available"); c == nil || c.Status != "True" {
		t.Fatalf("Available condition wrong: %+v", c)
	}
}

func TestReconcileDegradedPartialReady(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 1) // 1 of 2 serving
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseDegraded || !res.Requeue {
		t.Fatalf("partial ready: phase=%s requeue=%v, want Degraded/true", res.Phase, res.Requeue)
	}
	if got := reg.Get("default/iris-mlp").Deployment.Status.ReadyReplicas; got != 1 {
		t.Fatalf("ReadyReplicas = %d, want 1", got)
	}
}

func TestReconcileRollingUpdate(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	// New image, new generation: a rolling update in place.
	d := dep("iris-mlp", 2)
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d.Spec.Model.Version = "v2"
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionUpdateDeployment {
		t.Fatalf("actions = %v, want a single UpdateDeployment", res.Actions)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("live image = %s, want v2", got.Container.Image)
	}
	// Pods restart on the new image: ready drops, phase regresses.
	cluster.SetReady("default/iris-mlp", 0)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseProgressing {
		t.Fatalf("after rollout restart: phase=%s, want Progressing", res.Phase)
	}
	if got := reg.Get("default/iris-mlp").Deployment.Status.ObservedGeneration; got != 2 {
		t.Fatalf("ObservedGeneration = %d, want 2", got)
	}
}

func TestReconcileRecreateDeletesFirst(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Strategy = api.StrategyRecreate
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)

	d2 := dep("iris-mlp", 2)
	d2.Spec.Strategy = api.StrategyRecreate
	d2.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d2.Spec.Model.Version = "v2"
	reg.Register(d2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 2 { // DeleteDeployment + CreateDeployment; the Service is unchanged
		t.Fatalf("actions = %v, want Delete + Create deployment", res.Actions)
	}
	if res.Actions[0].Kind != ActionDeleteDeployment || res.Actions[1].Kind != ActionCreateDeployment {
		t.Fatalf("recreate order wrong: %v", res.Actions)
	}
	// A replica-count change under Recreate is still an in-place update.
	d3 := dep("iris-mlp", 3)
	d3.Spec.Strategy = api.StrategyRecreate
	d3.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d3.Spec.Model.Version = "v2"
	replicas := int32(4)
	d3.Spec.Replicas = &replicas
	reg.Register(d3)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// A scale-up changes the Deployment and the PDB (minAvailable
	// is replicas-1 by default, so 2 -> 4 replicas moves it 1 -> 3).
	if len(res.Actions) != 2 || res.Actions[0].Kind != ActionUpdateDeployment || res.Actions[1].Kind != ActionUpdatePDB {
		t.Fatalf("scale under Recreate: actions = %v, want [UpdateDeployment UpdatePDB]", res.Actions)
	}
}

func TestReconcileCanary(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 25, Steps: 4, IntervalSeconds: 30}
	reg.Register(d)

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 4 { // stable + canary + service + PDB
		t.Fatalf("actions = %v, want 4", res.Actions)
	}
	canary, ok := cluster.GetDeployment("default/iris-mlp-canary")
	if !ok {
		t.Fatal("canary Deployment was not created")
	}
	if !canary.Canary || canary.Replicas != 1 { // 25% of 2 rounds to 1
		t.Fatalf("canary wrong: %+v", canary)
	}
	// The Service selector carries no canary label, so it matches
	// both stable and canary pods and traffic splits by pod count.
	svc, _ := cluster.GetService("default/iris-mlp")
	if _, ok := svc.Selector["ml-operator/canary"]; ok {
		t.Fatal("service selector must not carry the canary label")
	}

	// Strategy moves to RollingUpdate: the canary is removed. The
	// stable Deployment is unchanged (strategy is not a rendered
	// field), so the canary delete is the only action.
	d2 := dep("iris-mlp", 2)
	reg.Register(d2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionDeleteDeployment {
		t.Fatalf("actions = %v, want a single canary DeleteDeployment", res.Actions)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); ok {
		t.Fatal("canary Deployment was not removed after strategy change")
	}
}

func TestReconcileInvalidSpecFails(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Model.Image = "" // invalid: image required
	reg.Register(d)

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseFailed || res.Requeue {
		t.Fatalf("invalid spec: phase=%s requeue=%v, want Failed/false", res.Phase, res.Requeue)
	}
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatal("invalid spec reached the cluster")
	}
	if got := reg.Get("default/iris-mlp").Deployment.Status.Message; got == "" {
		t.Fatal("Failed status has no message")
	}
}

func TestReconcileDeletedGarbageCollects(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 25, Steps: 4, IntervalSeconds: 30}
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if cluster.DeploymentCount() != 2 || cluster.ServiceCount() != 1 {
		t.Fatalf("setup: %d deployments / %d services", cluster.DeploymentCount(), cluster.ServiceCount())
	}

	reg.Delete("default/iris-mlp")
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 4 { // stable + canary + service + PDB deletes
		t.Fatalf("actions = %v, want 4 deletes", res.Actions)
	}
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatal("cluster objects were not garbage-collected")
	}

	// Reconciling a key that was never registered is a clean NoOp.
	res, err = rec.Reconcile("default/ghost")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("ghost key: actions = %v, want a single NoOp", res.Actions)
	}
}

func TestReconcileServiceUpdate(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	// A port change updates the Deployment and the Service.
	d := dep("iris-mlp", 2)
	d.Spec.Port = 9000
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	kinds := map[ActionKind]bool{}
	for _, a := range res.Actions {
		kinds[a.Kind] = true
	}
	if !kinds[ActionUpdateDeployment] || !kinds[ActionUpdateService] {
		t.Fatalf("actions = %v, want deployment + service updates", res.Actions)
	}
	if got, _ := cluster.GetService("default/iris-mlp"); got.TargetPort != 9000 {
		t.Fatalf("live service targetPort = %d, want 9000", got.TargetPort)
	}
}

// TestConditionsKeepTransitionTimeAcrossReconciles pins the point of
// the transition-time work: a no-op reconcile must not re-stamp the
// conditions. The Ready condition is stamped when the deployment
// first becomes Progressing, keeps that stamp through a steady
// reconcile, moves only when the phase flips to Available, and then
// stays put through another steady reconcile. The clock is the
// reconciler's controllable Now, so the times are exact.
func TestConditionsKeepTransitionTimeAcrossReconciles(t *testing.T) {
	reg, cluster, rec := setup(t)
	t0 := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	now := t0
	rec.Now = func() time.Time { return now }

	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	ready := reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Ready")
	if ready == nil || ready.Status != "False" || ready.Reason != "Provisioning" {
		t.Fatalf("after create: Ready = %+v, want False/Provisioning", ready)
	}
	if !ready.LastTransitionTime.Equal(t0) {
		t.Fatalf("after create: Ready stamped %v, want %v", ready.LastTransitionTime, t0)
	}

	// Steady reconcile at a later clock: same phase, stamp must not move.
	now = t0.Add(10 * time.Minute)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Ready"); !got.LastTransitionTime.Equal(t0) {
		t.Fatalf("no-op reconcile moved the stamp: %v, want %v", got.LastTransitionTime, t0)
	}

	// Pods come up: the flip to Available re-stamps.
	t2 := t0.Add(20 * time.Minute)
	now = t2
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	ready = reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Ready")
	if ready == nil || ready.Status != "True" || ready.Reason != "ReplicasAvailable" {
		t.Fatalf("after ready: Ready = %+v, want True/ReplicasAvailable", ready)
	}
	if !ready.LastTransitionTime.Equal(t2) {
		t.Fatalf("after ready: Ready stamped %v, want %v", ready.LastTransitionTime, t2)
	}

	// Another steady reconcile: the new stamp holds.
	now = t2.Add(10 * time.Minute)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Ready"); !got.LastTransitionTime.Equal(t2) {
		t.Fatalf("steady Available reconcile moved the stamp: %v, want %v", got.LastTransitionTime, t2)
	}
}

// TestConditionsCarryRealReasons checks the two failure paths that
// used to report the bare phase name as the condition reason: an
// invalid spec fails with InvalidSpec, and an exhausted apply retry
// degrades with ApplyFailed.
func TestConditionsCarryRealReasons(t *testing.T) {
	reg, _, rec := setup(t)
	bad := dep("iris-mlp", 1)
	bad.Spec.Model.Image = ""
	reg.Register(bad)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseFailed {
		t.Fatalf("invalid spec: phase=%s, want Failed", res.Phase)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	for _, typ := range []string{"Ready", "Progressing", "Available"} {
		if c := st.ConditionFor(typ); c == nil || c.Reason != "InvalidSpec" {
			t.Fatalf("invalid spec: %s condition = %+v, want reason InvalidSpec", typ, c)
		}
	}

	reg2, cluster2, rec2 := setup(t)
	rec2.ApplyRetryBackoff = 0
	reg2.Register(dep("iris-mlp", 1))
	cluster2.FailNextApplies(100)
	res, err = rec2.Reconcile("default/iris-mlp")
	if err == nil || res.Phase != api.PhaseDegraded {
		t.Fatalf("apply failure: err=%v phase=%s, want error + Degraded", err, res.Phase)
	}
	st2 := reg2.Get("default/iris-mlp").Deployment.Status
	if c := st2.ConditionFor("Ready"); c == nil || c.Reason != "ApplyFailed" {
		t.Fatalf("apply failure: Ready condition = %+v, want reason ApplyFailed", c)
	}
}

// TestReconcilePaused freezes a deployment: while Paused is true the
// reconciler writes nothing to the cluster (not even a spec change
// that lands mid-pause), keeps the phase, and reports a Paused=True
// condition. Resuming reconciles normally and applies the change
// that was held. The ReconciliationPaused event fires once, on the
// transition into paused — later reconciles must not spam it.
func TestReconcilePaused(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if st := reg.Get("default/iris-mlp").Deployment.Status; st.Phase != api.PhaseAvailable {
		t.Fatalf("before pause: phase=%s, want Available", st.Phase)
	}

	// Pause at generation 2.
	paused := dep("iris-mlp", 2)
	paused.Spec.Paused = true
	paused.Status = reg.Get("default/iris-mlp").Deployment.Status
	reg.Register(paused)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile paused: %v", err)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("paused reconcile applied %v, want no actions", res.Actions)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if st.Phase != api.PhaseAvailable {
		t.Fatalf("paused: phase=%s, want Available preserved", st.Phase)
	}
	if c := st.ConditionFor("Paused"); c == nil || c.Status != "True" || c.Reason != "Paused" {
		t.Fatalf("paused: Paused condition = %+v, want True/Paused", c)
	}
	if evs := rec.Events.EventsFor("default/iris-mlp"); len(evs) != 1 || evs[0].Reason != "ReconciliationPaused" {
		t.Fatalf("paused: events = %+v, want exactly one ReconciliationPaused", evs)
	}

	// A spec change while paused is held, not applied.
	held := dep("iris-mlp", 3)
	held.Spec.Paused = true
	held.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	held.Spec.Model.Version = "v2"
	held.Status = st
	reg.Register(held)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	if live.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("paused: live image = %s, want v1 still serving", live.Container.Image)
	}
	if evs := rec.Events.EventsFor("default/iris-mlp"); len(evs) != 1 {
		t.Fatalf("second paused reconcile recorded another event: %+v", evs)
	}

	// Resume: the held change applies on the next reconcile.
	resumed := dep("iris-mlp", 4)
	resumed.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	resumed.Spec.Model.Version = "v2"
	resumed.Status = reg.Get("default/iris-mlp").Deployment.Status
	reg.Register(resumed)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile resumed: %v", err)
	}
	// The fake cluster keeps the replicas ready through the
	// rolling update, so the resumed deployment is Available
	// immediately — what matters is the image actually changed.
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("resumed: phase=%s, want Available (v2 applied, replicas ready)", res.Phase)
	}
	live, _ = cluster.GetDeployment("default/iris-mlp")
	if live.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("resumed: live image = %s, want v2", live.Container.Image)
	}
	if c := reg.Get("default/iris-mlp").Deployment.Status.ConditionFor("Paused"); c == nil || c.Status != "False" {
		t.Fatalf("resumed: Paused condition = %+v, want False", c)
	}
}

// TestReconcilePausedBeforeFirstDeploy: pausing a deployment that
// was never provisioned creates nothing in the cluster and keeps
// the Pending phase.
func TestReconcilePausedBeforeFirstDeploy(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Paused = true
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 0 || cluster.DeploymentCount() != 0 {
		t.Fatalf("paused fresh deploy: actions=%v deployments=%d, want none/0", res.Actions, cluster.DeploymentCount())
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if st.Phase != api.PhasePending {
		t.Fatalf("paused fresh deploy: phase=%s, want Pending", st.Phase)
	}
	if c := st.ConditionFor("Paused"); c == nil || c.Status != "True" {
		t.Fatalf("paused fresh deploy: Paused condition = %+v, want True", c)
	}
}
