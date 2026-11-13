package controller

import (
	"testing"

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

func setup(t *testing.T) (*registry.Registry, *FakeCluster, *Reconciler) {
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
	if len(res.Actions) != 2 { // CreateDeployment + CreateService
		t.Fatalf("actions = %v, want 2 (create deployment + service)", res.Actions)
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
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionUpdateDeployment {
		t.Fatalf("scale under Recreate: actions = %v, want a single UpdateDeployment", res.Actions)
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
	if len(res.Actions) != 3 { // stable + canary + service
		t.Fatalf("actions = %v, want 3", res.Actions)
	}
	canary, ok := cluster.GetDeployment("default/iris-mlp-canary")
	if !ok {
		t.Fatal("canary Deployment was not created")
	}
	if !canary.Canary || canary.Replicas != 1 { // 25% of 2 rounds to 1
		t.Fatalf("canary wrong: %+v", canary)
	}
	// The Service still selects stable pods only.
	svc, _ := cluster.GetService("default/iris-mlp")
	if _, ok := svc.Selector["ml-operator/canary"]; ok {
		t.Fatal("service selector matches canary pods")
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
	if len(res.Actions) != 3 { // stable + canary + service deletes
		t.Fatalf("actions = %v, want 3 deletes", res.Actions)
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
