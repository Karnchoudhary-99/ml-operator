package controller

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

// A transient cluster failure must not degrade a deployment: the
// reconciler retries the apply with backoff and only gives up when
// the attempt budget is spent.
func TestApplyRetryRecovers(t *testing.T) {
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0 // instant retries in tests
	reg.Register(dep("iris-mlp", 1))

	// The first two mutating calls fail; the third try of the
	// Deployment create succeeds, and the Service create is clean.
	cluster.FailNextApplies(2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing", res.Phase)
	}
	if cluster.DeploymentCount() != 1 || cluster.ServiceCount() != 1 {
		t.Fatalf("cluster holds %d deployments / %d services, want 1/1",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "recovered"); got != 1 {
		t.Fatalf("recovered retries = %v, want 1", got)
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "exhausted"); got != 0 {
		t.Fatalf("exhausted retries = %v, want 0", got)
	}
	found := false
	for _, e := range rec.Events.EventsFor("default/iris-mlp") {
		if e.Reason == "ApplyRetried" && strings.Contains(e.Message, "succeeded after 2 retries") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ApplyRetried event after 2 retries: %v", rec.Events.EventsFor("default/iris-mlp"))
	}
}

func TestApplyRetryExhausts(t *testing.T) {
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0
	reg.Register(dep("iris-mlp", 1))

	// Exactly the attempt budget fails: the reconcile degrades
	// and requeues, exactly as a permanently broken cluster should.
	cluster.FailNextApplies(3)
	res, err := rec.Reconcile("default/iris-mlp")
	if err == nil {
		t.Fatal("Reconcile succeeded despite 10 failing applies")
	}
	if res.Phase != api.PhaseDegraded || !res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Degraded/true", res.Phase, res.Requeue)
	}
	if cluster.DeploymentCount() != 0 {
		t.Fatalf("cluster holds %d deployments, want 0 (create never landed)", cluster.DeploymentCount())
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "exhausted"); got != 1 {
		t.Fatalf("exhausted retries = %v, want 1", got)
	}
	found := false
	for _, e := range rec.Events.EventsFor("default/iris-mlp") {
		if e.Reason == "ApplyFailed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ApplyFailed event after exhaustion: %v", rec.Events.EventsFor("default/iris-mlp"))
	}

	// The cluster recovers: the next reconcile lands the deployment.
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile after recovery: %v", err)
	}
	if res.Phase != api.PhaseProgressing || cluster.DeploymentCount() != 1 {
		t.Fatalf("after recovery: phase=%s deployments=%d, want Progressing/1",
			res.Phase, cluster.DeploymentCount())
	}
}

func TestApplyRetryIdempotent(t *testing.T) {
	// A retried update must land exactly once, with the right
	// content, and must not disturb the running Deployment:
	// re-applying does not reset readiness or duplicate objects.
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	d := dep("iris-mlp", 2)
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d.Spec.Model.Version = "v2"
	reg.Register(d)
	cluster.FailNextApplies(2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase = %s, want Available (ready pods survived the retry)", res.Phase)
	}
	if cluster.DeploymentCount() != 1 || cluster.ServiceCount() != 1 {
		t.Fatalf("cluster holds %d deployments / %d services, want 1/1",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
	live, _ := cluster.GetDeployment("default/iris-mlp")
	if live.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("live image = %s, want v2", live.Container.Image)
	}
	if got := cluster.ReadyReplicas("default/iris-mlp"); got != 2 {
		t.Fatalf("ready replicas = %d, want 2 (retry must not reset readiness)", got)
	}
}

func TestApplyRetryDeletedCleanup(t *testing.T) {
	// Garbage collection uses the same retry policy: a flaky
	// cluster must not leave a deleted deployment's objects
	// behind, and a permanently failing one must report the
	// cleanup as failed instead of pretending it worked.
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	reg.Delete("default/iris-mlp")

	cluster.FailNextApplies(2)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("cleanup reconcile: %v", err)
	}
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatalf("after cleanup: %d deployments / %d services, want 0/0",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "recovered"); got != 1 {
		t.Fatalf("recovered retries = %v, want 1", got)
	}
	if len(res.Actions) != 3 { // DeleteDeployment + DeleteService + DeletePDB
		t.Fatalf("cleanup actions = %v, want 3 deletes", res.Actions)
	}

	// Exhaustion: the cleanup fails and says so.
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	reg.Delete("default/iris-mlp")
	cluster.FailNextApplies(3)
	if _, err := rec.Reconcile("default/iris-mlp"); err == nil {
		t.Fatal("cleanup reconcile succeeded despite 3 failing deletes")
	}
	if cluster.DeploymentCount() != 1 {
		t.Fatalf("failed cleanup removed the Deployment anyway: %d left, want 1", cluster.DeploymentCount())
	}
	// The cluster recovers: the next reconcile finishes the cleanup.
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("cleanup after recovery: %v", err)
	}
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatalf("after recovery: %d deployments / %d services, want 0/0",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
}

func TestApplyRetryBounded(t *testing.T) {
	// The attempt budget is honoured exactly: with 3 attempts the
	// reconciler makes 3 tries and no more, even when the failure
	// count is far higher. A custom budget is respected too.
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0
	reg.Register(dep("iris-mlp", 1))

	cluster.FailNextApplies(100)
	if _, err := rec.Reconcile("default/iris-mlp"); err == nil {
		t.Fatal("Reconcile succeeded despite a permanently failing cluster")
	}
	// 3 attempts spent on the Deployment create; the Service create
	// was never reached, so exactly 3 failures were consumed.
	// Re-arming with 1 failure proves the counter state: the next
	// reconcile succeeds on the second try of the create.
	cluster.FailNextApplies(1)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile after re-arm: %v", err)
	}
	if res.Phase != api.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing", res.Phase)
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "recovered"); got != 1 {
		t.Fatalf("recovered retries = %v, want 1", got)
	}

	// A custom budget of 1 means no retry at all: one failure
	// degrades immediately.
	reg2, cluster2, rec2 := setup(t)
	rec2.ApplyMaxAttempts = 1
	rec2.ApplyRetryBackoff = 0
	reg2.Register(dep("iris-mlp", 1))
	cluster2.FailNextApplies(1)
	res, err = rec2.Reconcile("default/iris-mlp")
	if err == nil || res.Phase != api.PhaseDegraded {
		t.Fatalf("maxAttempts=1: err=%v phase=%s, want error + Degraded", err, res.Phase)
	}
}
