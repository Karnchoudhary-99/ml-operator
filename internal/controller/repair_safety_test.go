package controller

import (
	"strings"
	"testing"
)

func TestRepairNeverDeletesUnexpected(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// A leftover canary Deployment is in the cluster although none
	// is desired — a previous canary rollout, or someone's manual
	// apply. It might still be serving traffic.
	live, _ := cluster.GetDeployment("default/iris-mlp")
	canary := live
	canary.Name = "iris-mlp-canary"
	if err := cluster.ApplyDeployment(canary); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}
	// And the stable Deployment drifted at the same time, so the
	// repair has real work to do alongside the leftover.
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	rep, err := rec.Repair("default/iris-mlp", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	// The stable Deployment was fixed...
	res := repairResource(t, rep, "Deployment", "default/iris-mlp")
	if res.Outcome != RepairFixed {
		t.Fatalf("stable Deployment outcome=%s, want repaired", res.Outcome)
	}
	// ...but the leftover canary was reported, not deleted.
	left := repairResource(t, rep, "Deployment", "default/iris-mlp-canary")
	if left.Outcome != RepairLeft || left.Was != DriftUnexpected {
		t.Fatalf("canary outcome=%s was=%s, want left/unexpected", left.Outcome, left.Was)
	}
	if !strings.Contains(left.Detail, "never deletes") {
		t.Fatalf("detail should say repair never deletes, got %q", left.Detail)
	}
	if _, ok := cluster.GetDeployment("default/iris-mlp-canary"); !ok {
		t.Fatal("repair deleted the unexpected canary — it must never delete")
	}
	if !rep.AllFixed() {
		// AllFixed is false while an unexpected object stands:
		// the human still has a decision to make.
		t.Logf("AllFixed false as expected with a leftover object: %+v", rep.Results)
	} else {
		t.Fatal("want AllFixed false while an unexpected object stands")
	}
}

func TestRepairRefusesPaused(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Pause the deployment, then tamper with the live image.
	paused := dep("iris-mlp", 2)
	paused.Spec.Paused = true
	reg.Register(paused)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile paused: %v", err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	rep, err := rec.Repair("default/iris-mlp", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if !rep.Paused {
		t.Fatal("want Paused true in the report")
	}
	if rep.Fixed() != 0 {
		t.Fatalf("fixed=%d, want 0: repair must write nothing while paused", rep.Fixed())
	}
	res := repairResource(t, rep, "Deployment", "default/iris-mlp")
	if res.Outcome != RepairLeft {
		t.Fatalf("Deployment outcome=%s, want left", res.Outcome)
	}
	if !strings.Contains(res.Detail, "paused") {
		t.Fatalf("detail should name the pause, got %q", res.Detail)
	}
	still, _ := cluster.GetDeployment("default/iris-mlp")
	if still.Container.Image != "registry.example.com/iris-mlp:v99" {
		t.Fatalf("repair changed the live image while paused to %s", still.Container.Image)
	}
}

func TestRepairAllCoversEveryDeployment(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	reg.Register(dep("house-price", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile iris: %v", err)
	}
	if _, err := rec.Reconcile("default/house-price"); err != nil {
		t.Fatalf("Reconcile house: %v", err)
	}
	// Drift only the second deployment.
	live, _ := cluster.GetDeployment("default/house-price")
	live.Replicas = 1
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	reps, err := rec.RepairAll(false)
	if err != nil {
		t.Fatalf("RepairAll: %v", err)
	}
	if len(reps) != 2 {
		t.Fatalf("got %d reports, want 2", len(reps))
	}
	// Sorted by key, and only house-price needed a fix.
	if reps[0].Deployment != "default/house-price" || reps[1].Deployment != "default/iris-mlp" {
		t.Fatalf("reports not in key order: %q, %q", reps[0].Deployment, reps[1].Deployment)
	}
	if reps[0].Fixed() != 1 {
		t.Fatalf("house-price fixed=%d, want 1", reps[0].Fixed())
	}
	if reps[1].Fixed() != 0 {
		t.Fatalf("iris-mlp fixed=%d, want 0", reps[1].Fixed())
	}
}
