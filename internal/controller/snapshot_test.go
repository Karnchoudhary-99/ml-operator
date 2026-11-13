package controller

import (
	"encoding/json"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

func TestFakeClusterSnapshotRestore(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 140, RequestsPerSecond: -1, ErrorRate: -1})

	snap := cluster.Snapshot()
	if snap.Deployments["default/iris-mlp"].Container.Image == "" {
		t.Fatalf("snapshot lost the Deployment: %+v", snap.Deployments)
	}
	if snap.Ready["default/iris-mlp"] != 2 {
		t.Fatalf("snapshot ready = %d, want 2", snap.Ready["default/iris-mlp"])
	}
	if m, ok := snap.Metrics["default/iris-mlp"]; !ok || m.CPUUtilizationPercent != 140 {
		t.Fatalf("snapshot metrics = %+v, want CPU 140", m)
	}

	// A snapshot is a deep copy: mutating the live cluster after
	// the snapshot must not change it.
	cluster.SetReady("default/iris-mlp", 0)
	if snap.Ready["default/iris-mlp"] != 2 {
		t.Fatalf("snapshot mutated by later SetReady: %d", snap.Ready["default/iris-mlp"])
	}

	restored := NewFakeCluster()
	restored.Restore(snap)
	if _, ok := restored.GetDeployment("default/iris-mlp"); !ok {
		t.Fatalf("restored cluster lost the Deployment")
	}
	if _, ok := restored.GetService("default/iris-mlp"); !ok {
		t.Fatalf("restored cluster lost the Service")
	}
	if restored.ReadyReplicas("default/iris-mlp") != 2 {
		t.Fatalf("restored ready = %d, want 2", restored.ReadyReplicas("default/iris-mlp"))
	}
	if m, ok := restored.Metrics("default/iris-mlp"); !ok || m.CPUUtilizationPercent != 140 {
		t.Fatalf("restored metrics = %+v, want CPU 140", m)
	}
	// Restore is a copy too: the restored cluster and the snapshot
	// must not share maps.
	restored.SetReady("default/iris-mlp", 1)
	if snap.Ready["default/iris-mlp"] != 2 {
		t.Fatalf("snapshot mutated by restored cluster: %d", snap.Ready["default/iris-mlp"])
	}

	// An empty snapshot restores to an empty cluster without panicking.
	empty := NewFakeCluster()
	empty.Restore(ClusterSnapshot{})
	if empty.DeploymentCount() != 0 || empty.ReadyReplicas("default/iris-mlp") != 0 {
		t.Fatalf("empty restore left state behind")
	}
}

func TestReconcilerRolloutStateAccessors(t *testing.T) {
	reg, _, rec := setup(t)
	d := canaryDep(1, 10, 2, 30)
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rs, ok := rec.RolloutState("default/iris-mlp")
	if !ok {
		t.Fatalf("RolloutState missing after a canary reconcile")
	}
	if rs.Generation != 1 || len(rs.Weights) == 0 || rs.CurrentWeight() != 10 {
		t.Fatalf("rollout state wrong: %+v", rs)
	}
	if _, ok := rec.RolloutState("default/other"); ok {
		t.Fatalf("RolloutState returned a state for an unknown key")
	}
	all := rec.RolloutStates()
	if len(all) != 1 || all["default/iris-mlp"].CurrentWeight() != 10 {
		t.Fatalf("RolloutStates = %v", all)
	}

	// Restoring the state into a fresh reconciler over the same
	// registry resumes the rollout at the same step instead of
	// restarting it.
	_, cluster2, rec2 := setup(t)
	_ = cluster2
	reg2 := reg // same registry content
	rec2 = New(reg2, NewFakeCluster())
	rec2.RestoreRolloutState("default/iris-mlp", rs)
	rs2, ok := rec2.RolloutState("default/iris-mlp")
	if !ok || rs2.Step != rs.Step || rs2.Generation != 1 {
		t.Fatalf("restored rollout state = %+v (ok=%v), want step %d gen 1", rs2, ok, rs.Step)
	}

	// A stale restore (wrong generation, or a changed ladder) is
	// dropped: the next reconcile starts a fresh rollout.
	stale := rs
	stale.Generation = 99
	rec2.RestoreRolloutState("default/iris-mlp", stale)
	if rs3, _ := rec2.RolloutState("default/iris-mlp"); rs3.Generation == 99 {
		t.Fatalf("stale generation restore was accepted: %+v", rs3)
	}
	stale = rs
	stale.Weights = []int32{50, 100}
	rec2.RestoreRolloutState("default/iris-mlp", stale)
	if rs3, _ := rec2.RolloutState("default/iris-mlp"); len(rs3.Weights) == 2 && rs3.Weights[0] == 50 {
		t.Fatalf("changed-ladder restore was accepted: %+v", rs3)
	}
	// Restore for a deployment that is not registered is a no-op.
	rec2.RestoreRolloutState("default/missing", rs)
	if _, ok := rec2.RolloutState("default/missing"); ok {
		t.Fatalf("restore created state for an unregistered deployment")
	}
}

func TestRenderDeploymentJSONRoundTrip(t *testing.T) {
	// The state file stores rendered objects as JSON, so the
	// render types must survive a marshal/unmarshal round-trip.
	d := render.Deployment{
		Name: "iris-mlp", Namespace: "default", Replicas: 2,
		Labels:   map[string]string{"app": "iris-mlp"},
		Selector: map[string]string{"app": "iris-mlp"},
		Container: render.Container{
			Name: "model", Image: "img:v1", Port: 8080,
			Env:            []render.EnvVar{{Name: "LOG_LEVEL", Value: "info"}},
			ReadinessProbe: render.Probe{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var back render.Deployment
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if back.Name != "iris-mlp" || back.Container.Image != "img:v1" ||
		len(back.Container.Env) != 1 || back.Container.Env[0].Value != "info" ||
		back.Container.ReadinessProbe.Path != "/healthz" {
		t.Fatalf("round-trip lost data: %+v", back)
	}
}
