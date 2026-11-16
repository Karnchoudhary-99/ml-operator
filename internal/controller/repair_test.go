package controller

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

// repairResource finds one resource in a repair report, failing
// the test when it is absent.
func repairResource(t *testing.T, rep DeploymentRepair, kind, key string) ResourceRepair {
	t.Helper()
	for _, res := range rep.Results {
		if res.Kind == kind && res.Key == key {
			return res
		}
	}
	t.Fatalf("repair report for %s has no %s %s: %+v", rep.Deployment, kind, key, rep.Results)
	return ResourceRepair{}
}

func TestRepairDriftedDeployment(t *testing.T) {
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

	rep, err := rec.Repair("default/iris-mlp", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if !rep.AllFixed() {
		t.Fatalf("want all fixed, got %+v", rep.Results)
	}
	res := repairResource(t, rep, "Deployment", "default/iris-mlp")
	if res.Outcome != RepairFixed {
		t.Fatalf("Deployment outcome=%s, want repaired", res.Outcome)
	}
	if res.Was != DriftDrifted {
		t.Fatalf("Deployment was=%s, want drifted", res.Was)
	}
	if !strings.Contains(res.Detail, "v99") || !strings.Contains(res.Detail, "v1") {
		t.Fatalf("detail should name the image move, got %q", res.Detail)
	}

	// The live Deployment is back to the desired image and count.
	restored, _ := cluster.GetDeployment("default/iris-mlp")
	if restored.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("image=%s, want v1", restored.Container.Image)
	}
	if restored.Replicas != 2 {
		t.Fatalf("replicas=%d, want 2", restored.Replicas)
	}

	// And a fresh drift check agrees: everything is in sync.
	after, err := rec.Drift("default/iris-mlp")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if !after.InSync {
		t.Fatalf("want in sync after repair, got %+v", after.Resources)
	}

	// The repair is recorded as a DriftRepaired event (history
	// tracks generation/phase/image transitions, which repair
	// does not change).
	evs := rec.Events.EventsFor("default/iris-mlp")
	found := false
	for _, e := range evs {
		if e.Reason == "DriftRepaired" && strings.Contains(e.Message, "drift repaired") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a DriftRepaired event, got %+v", evs)
	}
}

func TestRepairMissingService(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Someone deleted the Service out of band.
	if err := cluster.DeleteService("default/iris-mlp"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	rep, err := rec.Repair("default/iris-mlp", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	res := repairResource(t, rep, "Service", "default/iris-mlp")
	if res.Outcome != RepairFixed || res.Was != DriftMissing {
		t.Fatalf("Service outcome=%s was=%s, want repaired/missing", res.Outcome, res.Was)
	}
	if _, ok := cluster.GetService("default/iris-mlp"); !ok {
		t.Fatal("service was not recreated")
	}
	// The untouched Deployment is listed as unchanged, not as a
	// repair: the report is a complete inventory.
	if d := repairResource(t, rep, "Deployment", "default/iris-mlp"); d.Outcome != RepairUnchanged {
		t.Fatalf("Deployment outcome=%s, want unchanged", d.Outcome)
	}
}

func TestRepairInSyncIsNoop(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rep, err := rec.Repair("default/iris-mlp", false)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if rep.Fixed() != 0 {
		t.Fatalf("fixed=%d, want 0 on an in-sync deployment", rep.Fixed())
	}
	if !rep.AllFixed() {
		t.Fatalf("want all fixed (nothing to do), got %+v", rep.Results)
	}
	// No history entry for a no-op repair: nothing happened.
	hist := rec.History("default/iris-mlp")
	for _, e := range hist {
		if strings.Contains(e.Reason, "drift repaired") {
			t.Fatalf("no-op repair must not record history, got %q", e.Reason)
		}
	}
}

func TestRepairDryRunChangesNothing(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	rep, err := rec.Repair("default/iris-mlp", true)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if !rep.DryRun {
		t.Fatal("want DryRun true")
	}
	res := repairResource(t, rep, "Deployment", "default/iris-mlp")
	if res.Outcome != RepairWouldFix {
		t.Fatalf("Deployment outcome=%s, want would-repair", res.Outcome)
	}
	// The live image is untouched by a dry run.
	still, _ := cluster.GetDeployment("default/iris-mlp")
	if still.Container.Image != "registry.example.com/iris-mlp:v99" {
		t.Fatalf("dry run changed the live image to %s", still.Container.Image)
	}
	if after, err := rec.Drift("default/iris-mlp"); err != nil || after.InSync {
		t.Fatalf("drift after dry run: inSync=%v err=%v, want still drifted", after.InSync, err)
	}
}

func TestRepairRecordsMetrics(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// One drifted object to fix, one unexpected object to leave.
	live, _ := cluster.GetDeployment("default/iris-mlp")
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}
	canary := live
	canary.Name = "iris-mlp-canary"
	if err := cluster.ApplyDeployment(canary); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}

	if _, err := rec.Repair("default/iris-mlp", false); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if got := rec.Metrics.Counter(metrics.MetricRepairsTotal, "outcome", "repaired"); got != 1 {
		t.Fatalf("repaired counter = %v, want 1", got)
	}
	if got := rec.Metrics.Counter(metrics.MetricRepairsTotal, "outcome", "left"); got != 1 {
		t.Fatalf("left counter = %v, want 1", got)
	}
	if got := rec.Metrics.Counter(metrics.MetricRepairsTotal, "outcome", "failed"); got != 0 {
		t.Fatalf("failed counter = %v, want 0", got)
	}
	if !strings.Contains(rec.Metrics.Render(), `ml_operator_repairs_total{outcome="repaired"} 1`) {
		t.Fatalf("Render() missing the repairs series:\n%s", rec.Metrics.Render())
	}

	// A dry run counts nothing, even when it would repair.
	live, _ = cluster.GetDeployment("default/iris-mlp")
	live.Container.Image = "registry.example.com/iris-mlp:v99"
	if err := cluster.ApplyDeployment(live); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}
	if _, err := rec.Repair("default/iris-mlp", true); err != nil {
		t.Fatalf("Repair dry-run: %v", err)
	}
	if got := rec.Metrics.Counter(metrics.MetricRepairsTotal, "outcome", "repaired"); got != 1 {
		t.Fatalf("repaired counter after dry run = %v, want still 1", got)
	}
}

func TestRepairUnknownDeployment(t *testing.T) {
	_, _, rec := setup(t)
	if _, err := rec.Repair("default/nope", false); err == nil {
		t.Fatal("want an error for an unknown deployment")
	}
}
