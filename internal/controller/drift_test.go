package controller

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// driftResource finds one resource in a report, failing the test
// when it is absent.
func driftResource(t *testing.T, rep DeploymentDrift, kind, key string) ResourceDrift {
	t.Helper()
	for _, res := range rep.Resources {
		if res.Kind == kind && res.Key == key {
			return res
		}
	}
	t.Fatalf("drift report for %s has no %s %s: %+v", rep.Deployment, kind, key, rep.Resources)
	return ResourceDrift{}
}

func TestDriftInSync(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rep, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if !rep.InSync {
		t.Fatalf("want in sync, got %+v", rep.Resources)
	}
	if rep.Paused {
		t.Fatalf("want Paused false")
	}
	for _, want := range []struct{ kind, key string }{
		{"Deployment", "default/iris-mlp"},
		{"Service", "default/iris-mlp"},
		{"PDB", "default/iris-mlp"},
	} {
		if res := driftResource(t, rep, want.kind, want.key); res.Status != DriftInSync {
			t.Fatalf("%s %s status=%s, want inSync", want.kind, want.key, res.Status)
		}
	}
	// Never-desired, never-present extras are not listed at all.
	for _, res := range rep.Resources {
		if strings.HasSuffix(res.Key, "-canary") || strings.HasSuffix(res.Key, "-green") || res.Kind == "HPA" {
			t.Fatalf("unexpected resource listed: %+v", res)
		}
	}
}

func TestDriftImageAndReplicasChanged(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Someone edited the live Deployment out of band: a new image
	// and a manual scale-down.
	live, ok := cluster.GetDeployment("default/iris-mlp")
	if !ok {
		t.Fatal("no live deployment")
	}
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	live.Replicas = 1
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	rep, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if rep.InSync {
		t.Fatal("want drift, got in sync")
	}
	res := driftResource(t, rep, "Deployment", "default/iris-mlp")
	if res.Status != DriftDrifted {
		t.Fatalf("Deployment status=%s, want drifted", res.Status)
	}
	joined := strings.Join(res.Reasons, " ")
	if !strings.Contains(joined, "v99") || !strings.Contains(joined, "v1") {
		t.Fatalf("reasons should name the image move, got %q", joined)
	}
	if !strings.Contains(joined, "replicas 1 -> 2") {
		t.Fatalf("reasons should name the replica move, got %q", joined)
	}
	// The Service was untouched, so it stays in sync.
	if svc := driftResource(t, rep, "Service", "default/iris-mlp"); svc.Status != DriftInSync {
		t.Fatalf("Service status=%s, want inSync", svc.Status)
	}
}

func TestDriftMissingObjects(t *testing.T) {
	reg, _, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	// Registered but never reconciled: nothing exists yet.
	rep, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if rep.InSync {
		t.Fatal("want drift, got in sync")
	}
	for _, want := range []struct{ kind, key string }{
		{"Deployment", "default/iris-mlp"},
		{"Service", "default/iris-mlp"},
		{"PDB", "default/iris-mlp"},
	} {
		if res := driftResource(t, rep, want.kind, want.key); res.Status != DriftMissing {
			t.Fatalf("%s %s status=%s, want missing", want.kind, want.key, res.Status)
		}
	}

	// Reconcile, then delete the Service out of band.
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// The fake cluster deletes through the same path the
	// reconciler's GC uses.
	if err := rec.Cluster.DeleteService("default/iris-mlp"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	rep, err = rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if res := driftResource(t, rep, "Service", "default/iris-mlp"); res.Status != DriftMissing {
		t.Fatalf("Service status=%s, want missing", res.Status)
	}
	if res := driftResource(t, rep, "Deployment", "default/iris-mlp"); res.Status != DriftInSync {
		t.Fatalf("Deployment status=%s, want inSync", res.Status)
	}
}

func TestDriftUnexpectedCanary(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// A leftover canary Deployment from an earlier rollout that
	// was never garbage-collected.
	canary := render.Deployment{
		Name: "iris-mlp-canary", Namespace: "default", Replicas: 1,
		Canary:    true,
		Container: render.Container{Name: "model", Image: "registry.example.com/iris-mlp:v2", Port: 8080},
	}
	if err := cluster.ApplyDeployment(canary); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	rep, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if rep.InSync {
		t.Fatal("want drift, got in sync")
	}
	res := driftResource(t, rep, "Deployment", "default/iris-mlp-canary")
	if res.Status != DriftUnexpected {
		t.Fatalf("canary status=%s, want unexpected", res.Status)
	}
	if len(res.Reasons) == 0 {
		t.Fatal("unexpected canary needs a reason")
	}
}

func TestDriftPausedStillReports(t *testing.T) {
	reg, _, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.Paused = true
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rep, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if !rep.Paused {
		t.Fatal("want Paused true")
	}
	// Paused reconcile applies nothing, so the Deployment is missing.
	if res := driftResource(t, rep, "Deployment", "default/iris-mlp"); res.Status != DriftMissing {
		t.Fatalf("Deployment status=%s, want missing", res.Status)
	}
}

func TestDriftUnknownKey(t *testing.T) {
	_, _, rec := setup(t)
	if _, err := rec.Drift("default/nope"); err == nil {
		t.Fatal("want an error for an unknown deployment")
	}
}

func TestDriftAll(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("zebra", 1))
	reg.Register(dep("alpha", 1))
	for _, key := range []string{"default/zebra", "default/alpha"} {
		if _, err := rec.Reconcile(key); err != nil {
			t.Fatalf("Reconcile %s: %v", key, err)
		}
	}

	// Tamper with one of the two.
	live, ok := cluster.GetDeployment("default/zebra")
	if !ok {
		t.Fatal("no live zebra deployment")
	}
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	reps, err := rec.DriftAll()
	if err != nil {
		t.Fatalf("DriftAll: %v", err)
	}
	if len(reps) != 2 {
		t.Fatalf("got %d reports, want 2", len(reps))
	}
	if reps[0].Deployment != "default/alpha" || reps[1].Deployment != "default/zebra" {
		t.Fatalf("reports not in key order: %q, %q", reps[0].Deployment, reps[1].Deployment)
	}
	if !reps[0].InSync || reps[1].InSync {
		t.Fatalf("alpha inSync=%v zebra inSync=%v, want true/false", reps[0].InSync, reps[1].InSync)
	}
}
