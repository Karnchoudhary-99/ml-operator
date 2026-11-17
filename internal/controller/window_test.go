package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// The maintenance-window tests pin the reconciler clock to Monday
// 2026-11-16 so the weekday arithmetic is checkable by hand: the
// window under test is Mon 02:00–04:00 UTC, closed at 09:00 and
// open at 03:00.
func mondayWindowDep(gen int64) api.ModelDeployment {
	d := dep("iris-mlp", gen)
	d.Spec.MaintenanceWindow = &api.MaintenanceWindowSpec{
		Days:  []string{"Mon"},
		Start: "02:00",
		End:   "04:00",
	}
	return d
}

func TestReconcileDefersCreateOutsideWindow(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(mondayWindowDep(1))

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if cluster.DeploymentCount() != 0 || cluster.ServiceCount() != 0 {
		t.Fatalf("cluster holds %d deployments / %d services, want 0/0 (nothing may change outside the window)",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
	if res.Phase != api.PhasePending || !res.Requeue {
		t.Fatalf("phase=%s requeue=%v, want Pending/true", res.Phase, res.Requeue)
	}
	if !strings.Contains(res.Message, "waiting for maintenance window") {
		t.Fatalf("message = %q, want it to name the wait", res.Message)
	}
	if !strings.Contains(res.Message, "Mon 02:00 UTC") {
		t.Fatalf("message = %q, want it to name the next opening", res.Message)
	}
	if !strings.Contains(res.Message, "Mon 02:00–04:00 UTC") {
		t.Fatalf("message = %q, want it to describe the window", res.Message)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "True" {
		t.Fatalf("WindowBlocked condition wrong: %+v", c)
	}
	// The deferral is an event, recorded once: a second reconcile
	// outside the window must not spam the log.
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	events := rec.Events.EventsFor("default/iris-mlp")
	blocked := 0
	for _, e := range events {
		if e.Reason == "WindowBlocked" {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("WindowBlocked events = %d, want 1 (recorded once, on the transition)", blocked)
	}
}

func TestReconcileAppliesInsideWindow(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(mondayWindowDep(1))

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if cluster.DeploymentCount() != 1 || cluster.ServiceCount() != 1 {
		t.Fatalf("cluster holds %d deployments / %d services, want 1/1 (window is open)",
			cluster.DeploymentCount(), cluster.ServiceCount())
	}
	if res.Phase != api.PhaseProgressing {
		t.Fatalf("phase=%s, want Progressing", res.Phase)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "False" {
		t.Fatalf("WindowBlocked condition wrong: %+v", c)
	}
}

func TestReconcileDefersImageChangeThenAppliesWhenWindowOpens(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(mondayWindowDep(1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	// 09:00, window closed: a new image must not land.
	now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	d := mondayWindowDep(2)
	d.Spec.Model = api.ModelSpec{Image: "registry.example.com/iris-mlp:v2", Framework: "onnx", Version: "v2"}
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 (change deferred)", got.Container.Image)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase=%s, want Available (the model keeps serving v1)", res.Phase)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "True" {
		t.Fatalf("WindowBlocked condition wrong: %+v", c)
	}

	// Next Monday 03:00, window open again: the same generation
	// applies, and the condition flips back to False.
	now = time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("stable image = %s, want v2 (window open)", got.Container.Image)
	}
	st = reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "False" {
		t.Fatalf("WindowBlocked condition wrong after the window opened: %+v", c)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase=%s, want Available (the fake cluster keeps the 2 ready pods across the re-apply)", res.Phase)
	}
}

func TestReconcileInSyncOutsideWindowIsNotBlocked(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(mondayWindowDep(1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	// 09:00, window closed, but nothing would change: the
	// reconcile is a plain status check, not a deferral.
	now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp", res.Actions)
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase=%s, want Available", res.Phase)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "False" {
		t.Fatalf("WindowBlocked condition wrong: %+v (nothing was blocked)", c)
	}
	if strings.Contains(res.Message, "waiting for maintenance window") {
		t.Fatalf("message = %q, want no wait named when nothing is deferred", res.Message)
	}
}

func TestReconcileDefersCanaryAdvanceOutsideWindow(t *testing.T) {
	reg, cluster, rec := setup(t)
	availableV1(t, reg, cluster, rec)

	// Start the canary inside the window: v2 at 10%.
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	d := canaryDep(2, 10, 2, 30)
	d.Spec.MaintenanceWindow = &api.MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "02:00", End: "04:00"}
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp-canary", 1)

	// 09:00, window closed, interval elapsed, gate passing: the
	// canary must stay at 10% — no advance, no promote.
	now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if canary, _ := cluster.GetDeployment("default/iris-mlp-canary"); canary.Replicas != 1 {
		t.Fatalf("canary replicas = %d, want 1 (still at 10%%)", canary.Replicas)
	}
	if got, _ := cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("stable image = %s, want v1 (no promotion outside the window)", got.Container.Image)
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "True" {
		t.Fatalf("WindowBlocked condition wrong: %+v", c)
	}

	// Next Monday 03:00, window open: the step the window held
	// back now runs.
	now = time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if canary, _ := cluster.GetDeployment("default/iris-mlp-canary"); canary.Replicas != 1 {
		t.Fatalf("canary replicas = %d, want 1 (55%% of 2 rounds to 1)", canary.Replicas)
	}
	if !strings.Contains(res.Message, "advanced to 55%") {
		t.Fatalf("message = %q, want the held-back step to run", res.Message)
	}
}

func TestReconcileOvernightWindow(t *testing.T) {
	reg, cluster, rec := setup(t)
	d := dep("iris-mlp", 1)
	d.Spec.MaintenanceWindow = &api.MaintenanceWindowSpec{
		Days:  []string{"Mon"},
		Start: "22:00",
		End:   "02:00",
	}
	// Tuesday 01:00 is inside Monday's overnight window.
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(d)

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if cluster.DeploymentCount() != 1 {
		t.Fatalf("cluster holds %d deployments, want 1 (Tue 01:00 is inside Mon's overnight window)",
			cluster.DeploymentCount())
	}
}

func TestReconcileNoWindowIsUnrestricted(t *testing.T) {
	reg, cluster, rec := setup(t)
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return now }
	reg.Register(dep("iris-mlp", 1))

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if cluster.DeploymentCount() != 1 {
		t.Fatalf("cluster holds %d deployments, want 1 (no window means no restriction)",
			cluster.DeploymentCount())
	}
	st := reg.Get("default/iris-mlp").Deployment.Status
	if c := st.ConditionFor("WindowBlocked"); c == nil || c.Status != "False" {
		t.Fatalf("WindowBlocked condition wrong: %+v", c)
	}
}
