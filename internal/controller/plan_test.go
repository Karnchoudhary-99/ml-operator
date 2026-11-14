package controller

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// Plan is the read-only half of Reconcile: it must return exactly
// the actions Reconcile would execute, while leaving the cluster,
// the registry status, the metrics, the events and the rollout
// state untouched. These tests assert both halves — the plan
// contents and the absence of side effects.
func TestPlanFreshDeployment(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 2 || actions[0].Kind != ActionCreateDeployment || actions[1].Kind != ActionCreateService {
		t.Fatalf("plan = %v, want [CreateDeployment CreateService]", actions)
	}
	// Nothing changed: the cluster is still empty and the registry
	// status is still blank (no phase, no observed generation).
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatalf("Plan mutated the cluster: %d deployments / %d services", cluster.DeploymentCount(), cluster.ServiceCount())
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if st.Phase != "" || st.ObservedGeneration != 0 {
		t.Fatalf("Plan wrote status: %+v", st)
	}
	if got := rec.Events.Len(); got != 0 {
		t.Fatalf("Plan recorded %d events, want 0", got)
	}
	// A second Plan agrees: planning is deterministic and stateless.
	again, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if len(again) != len(actions) {
		t.Fatalf("second plan = %v, want %v", again, actions)
	}
}

func TestPlanInSyncIsNoOp(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	eventsBefore := rec.Events.Len()
	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != ActionNoOp {
		t.Fatalf("plan = %v, want a single NoOp", actions)
	}
	if rec.Events.Len() != eventsBefore {
		t.Fatalf("Plan recorded events: %d -> %d", eventsBefore, rec.Events.Len())
	}
}

func TestPlanImageChange(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	v2 := dep("iris-mlp", 2)
	v2.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	v2.Spec.Model.Version = "v2"
	reg.Register(v2)

	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != ActionUpdateDeployment {
		t.Fatalf("plan = %v, want a single UpdateDeployment", actions)
	}
	if !strings.Contains(actions[0].Reason, "v1") || !strings.Contains(actions[0].Reason, "v2") {
		t.Fatalf("plan reason %q should name both images", actions[0].Reason)
	}
	// The live Deployment still serves v1: Plan applied nothing.
	live, ok := cluster.GetDeployment("default/iris-mlp")
	if !ok || live.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("Plan changed the live image: %+v", live)
	}
}

func TestPlanCanaryFresh(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 10, Steps: 2, IntervalSeconds: 30}
	reg.Register(d)

	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Stable create + canary create + service create.
	if len(actions) != 3 {
		t.Fatalf("plan = %v, want 3 actions", actions)
	}
	if actions[0].Kind != ActionCreateDeployment || actions[0].Key != "default/iris-mlp" {
		t.Fatalf("first action = %+v, want CreateDeployment default/iris-mlp", actions[0])
	}
	if actions[1].Kind != ActionCreateDeployment || actions[1].Key != "default/iris-mlp-canary" {
		t.Fatalf("second action = %+v, want CreateDeployment default/iris-mlp-canary", actions[1])
	}
	if actions[2].Kind != ActionCreateService {
		t.Fatalf("third action = %+v, want CreateService", actions[2])
	}
	if cluster.DeploymentCount() != 0 {
		t.Fatalf("Plan created %d deployments, want 0", cluster.DeploymentCount())
	}
	// No rollout state was stored: a fresh Plan still starts at the
	// first weight, and the reconciler tracks no rollout.
	if _, ok := rec.RolloutState("default/iris-mlp"); ok {
		t.Fatalf("Plan stored rollout state")
	}
}

func TestPlanAutoscalingUsesLiveCount(t *testing.T) {
	reg, _, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: 1, MaxReplicas: 4, TargetCPUUtilization: 50}
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	// spec.replicas moves 2 -> 4, but the scaler owns the count while
	// autoscaling is on, so the plan must not chase the spec value.
	v2 := dep("iris-mlp", 2)
	v2.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: 1, MaxReplicas: 4, TargetCPUUtilization: 50}
	replicas := int32(4)
	v2.Spec.Replicas = &replicas
	reg.Register(v2)

	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != ActionNoOp {
		t.Fatalf("plan = %v, want a single NoOp (scaler owns replicas)", actions)
	}
}

func TestPlanErrors(t *testing.T) {
	_, _, rec := setup(t)
	if _, err := rec.Plan("default/no-such-model"); err == nil {
		t.Fatalf("Plan on an unknown deployment should error")
	}

	reg, _, rec2 := setup(t)
	bad := dep("bad-model", 1)
	bad.Spec.Model.Image = ""
	reg.Register(bad)
	if _, err := rec2.Plan("default/bad-model"); err == nil {
		t.Fatalf("Plan on an invalid spec should error")
	}
}
