package controller

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// TestFakeClusterScaleDownClampsReady: re-applying a Deployment
// with fewer replicas terminates pods, so the stored ready count
// clamps to the new replica count — a Deployment scaled to 0
// reports 0 ready, not its old pods. A scale-up never raises
// readiness: the new pods are not ready until SetReady says so.
func TestFakeClusterScaleDownClampsReady(t *testing.T) {
	_, cluster, _ := setup(t)
	key := "default/iris-mlp"
	dep := func(replicas int32) render.Deployment {
		return render.Deployment{
			Name: "iris-mlp", Namespace: "default", Replicas: replicas,
			Labels:   map[string]string{"app": "iris-mlp"},
			Selector: map[string]string{"app": "iris-mlp"},
			Container: render.Container{
				Name: "model", Image: "img:v1", Port: 8080,
			},
		}
	}

	if err := cluster.ApplyDeployment(dep(2)); err != nil {
		t.Fatalf("ApplyDeployment: %v", err)
	}
	if got := cluster.ReadyReplicas(key); got != 0 {
		t.Fatalf("new Deployment ready = %d, want 0", got)
	}
	cluster.SetReady(key, 2)
	if err := cluster.ApplyDeployment(dep(4)); err != nil {
		t.Fatalf("ApplyDeployment (scale up): %v", err)
	}
	if got := cluster.ReadyReplicas(key); got != 2 {
		t.Fatalf("after scale up ready = %d, want 2 (new pods not ready yet)", got)
	}
	cluster.SetReady(key, 4)
	if err := cluster.ApplyDeployment(dep(1)); err != nil {
		t.Fatalf("ApplyDeployment (scale down): %v", err)
	}
	if got := cluster.ReadyReplicas(key); got != 1 {
		t.Fatalf("after scale down ready = %d, want 1 (terminated pods are not ready)", got)
	}
	if err := cluster.ApplyDeployment(dep(0)); err != nil {
		t.Fatalf("ApplyDeployment (scale to zero): %v", err)
	}
	if got := cluster.ReadyReplicas(key); got != 0 {
		t.Fatalf("after scale to zero ready = %d, want 0", got)
	}
}

// TestFakeClusterModelLoadProbe checks the probe controls the
// reconciler's gate reads: loading / loaded / failed transitions,
// no-op for unknown Deployments, and survival across a
// snapshot/restore round-trip (the mlop CLI persists this state).
func TestFakeClusterModelLoadProbe(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// No probe report yet: Metrics has no entry for the key.
	if _, ok := cluster.Metrics("default/iris-mlp"); ok {
		t.Fatalf("metrics exist before any probe or observation")
	}

	// Loading: probe reported, model not loaded, not failed.
	cluster.SetModelLoading("default/iris-mlp")
	m, ok := cluster.Metrics("default/iris-mlp")
	if !ok || !m.ModelLoadKnown || m.ModelLoaded || m.ModelLoadFailed {
		t.Fatalf("loading probe = %+v, want known=true loaded=false failed=false", m)
	}
	if m.ErrorRate != -1 {
		t.Fatalf("fresh probe entry error rate = %v, want -1 (unknown)", m.ErrorRate)
	}

	// Loaded: probe reported success; the failed flag stays clear.
	cluster.SetModelLoaded("default/iris-mlp")
	m, _ = cluster.Metrics("default/iris-mlp")
	if !m.ModelLoadKnown || !m.ModelLoaded || m.ModelLoadFailed {
		t.Fatalf("loaded probe = %+v, want known=true loaded=true failed=false", m)
	}

	// Failed: probe reported a load failure (overrides loaded).
	cluster.SetModelLoadFailed("default/iris-mlp")
	m, _ = cluster.Metrics("default/iris-mlp")
	if !m.ModelLoadKnown || m.ModelLoaded || !m.ModelLoadFailed {
		t.Fatalf("failed probe = %+v, want known=true loaded=false failed=true", m)
	}

	// Probe setters keep an existing load observation instead of
	// replacing it: CPU 140 recorded before must survive.
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 140, RequestsPerSecond: -1, ErrorRate: -1})
	cluster.SetModelLoading("default/iris-mlp")
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.CPUUtilizationPercent != 140 {
		t.Fatalf("probe setter clobbered metrics: CPU = %v, want 140", m.CPUUtilizationPercent)
	}
	if !m.ModelLoadKnown || m.ModelLoaded {
		t.Fatalf("probe after SetMetrics = %+v, want loading state", m)
	}

	// Probe state survives a snapshot/restore round-trip.
	snap := cluster.Snapshot()
	restored := NewFakeCluster()
	restored.Restore(snap)
	m, ok = restored.Metrics("default/iris-mlp")
	if !ok || !m.ModelLoadKnown || m.ModelLoaded || m.ModelLoadFailed {
		t.Fatalf("restored probe = %+v, want loading state", m)
	}
	if m.CPUUtilizationPercent != 140 {
		t.Fatalf("restored CPU = %v, want 140", m.CPUUtilizationPercent)
	}

	// Probe setters are no-ops for a Deployment the fake never saw.
	cluster.SetModelLoaded("default/ghost")
	if _, ok := cluster.Metrics("default/ghost"); ok {
		t.Fatalf("probe setter created metrics for an unknown Deployment")
	}

	// Deleting the Deployment clears the probe state with it.
	if err := cluster.DeleteDeployment("default/iris-mlp"); err != nil {
		t.Fatalf("DeleteDeployment: %v", err)
	}
	if _, ok := cluster.Metrics("default/iris-mlp"); ok {
		t.Fatalf("metrics survived DeleteDeployment")
	}
}

// TestFakeClusterTraffic covers SetTraffic: observations
// accumulate into cumulative counters, ErrorRate is always the
// cumulative ratio, the latest p95 wins, and the probe/CPU state
// recorded alongside survives.
func TestFakeClusterTraffic(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// No traffic yet: no metrics entry at all.
	if _, ok := cluster.Metrics("default/iris-mlp"); ok {
		t.Fatalf("metrics exist before any traffic")
	}

	// First batch: 1000 requests, 10 errors, p95 180ms.
	cluster.SetTraffic("default/iris-mlp", 1000, 10, 180)
	m, ok := cluster.Metrics("default/iris-mlp")
	if !ok {
		t.Fatal("no metrics after SetTraffic")
	}
	if m.Requests != 1000 || m.Errors != 10 || m.ErrorRate != 0.01 || m.LatencyP95Ms != 180 {
		t.Fatalf("first batch = %+v, want 1000 requests, 10 errors, 0.01 rate, p95 180", m)
	}

	// Second batch accumulates: 1000 more, 30 errors, p95 240ms.
	// The rate is the cumulative 40/2000, not the second batch's
	// 0.03, and the p95 is the latest observation.
	cluster.SetTraffic("default/iris-mlp", 1000, 30, 240)
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.Requests != 2000 || m.Errors != 40 || m.ErrorRate != 0.02 || m.LatencyP95Ms != 240 {
		t.Fatalf("second batch = %+v, want 2000 requests, 40 errors, 0.02 rate, p95 240", m)
	}

	// A batch with no p95 leaves the previous p95 alone.
	cluster.SetTraffic("default/iris-mlp", 500, 0, 0)
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.Requests != 2500 || m.Errors != 40 || m.ErrorRate != 0.016 || m.LatencyP95Ms != 240 {
		t.Fatalf("no-p95 batch = %+v, want 2500 requests, 40 errors, 0.016 rate, p95 240", m)
	}

	// Traffic keeps the probe and CPU state recorded alongside it
	// (SetMetrics replaces the entry, so the probe is recorded
	// after it — the same order the CLI uses).
	cluster.SetMetrics("default/iris-mlp", ClusterMetrics{CPUUtilizationPercent: 140, RequestsPerSecond: -1, ErrorRate: m.ErrorRate, LatencyP95Ms: m.LatencyP95Ms, Requests: m.Requests, Errors: m.Errors})
	cluster.SetModelLoaded("default/iris-mlp")
	cluster.SetTraffic("default/iris-mlp", 100, 5, 200)
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.CPUUtilizationPercent != 140 || !m.ModelLoadKnown || !m.ModelLoaded {
		t.Fatalf("traffic clobbered probe/CPU state: %+v", m)
	}
	if m.Requests != 2600 || m.Errors != 45 {
		t.Fatalf("counters after probe/CPU batch = %d/%d, want 2600/45", m.Requests, m.Errors)
	}

	// Traffic survives a snapshot/restore round-trip (the mlop CLI
	// persists this state between invocations).
	snap := cluster.Snapshot()
	restored := NewFakeCluster()
	restored.Restore(snap)
	m, ok = restored.Metrics("default/iris-mlp")
	if !ok || m.Requests != 2600 || m.Errors != 45 || m.ErrorRate != float64(45)/float64(2600) || m.LatencyP95Ms != 200 {
		t.Fatalf("restored traffic = %+v, want 2600/45 cumulative", m)
	}

	// Defensive cases: no requests, negative errors, and more
	// errors than requests all leave the counters untouched or
	// clamped — they never corrupt the rate.
	cluster.SetTraffic("default/iris-mlp", 0, 0, 500)
	cluster.SetTraffic("default/iris-mlp", 100, -5, 500)
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.Requests != 2700 || m.Errors != 45 {
		t.Fatalf("defensive batches = %d/%d, want 2700/45", m.Requests, m.Errors)
	}
	cluster.SetTraffic("default/iris-mlp", 100, 500, 500)
	m, _ = cluster.Metrics("default/iris-mlp")
	if m.Requests != 2800 || m.Errors != 145 || m.ErrorRate != float64(145)/float64(2800) {
		t.Fatalf("over-error batch = %+v, want errors clamped to requests", m)
	}

	// Traffic is a no-op for a Deployment the fake never saw.
	cluster.SetTraffic("default/ghost", 100, 0, 100)
	if _, ok := cluster.Metrics("default/ghost"); ok {
		t.Fatalf("SetTraffic created metrics for an unknown Deployment")
	}
}

// TestFakeClusterPDB covers the PDB surface: apply/get/delete,
// delete of a missing PDB is a no-op, and PDBs survive a
// snapshot/restore round-trip (the mlop CLI persists this state).
func TestFakeClusterPDB(t *testing.T) {
	cluster := NewFakeCluster()
	if _, ok := cluster.GetPDB("default/iris-mlp"); ok {
		t.Fatal("PDB exists before any apply")
	}
	if err := cluster.DeletePDB("default/iris-mlp"); err != nil {
		t.Fatalf("DeletePDB of a missing PDB: %v", err)
	}
	pdb := render.PodDisruptionBudget{
		Name: "iris-mlp", Namespace: "default",
		Labels:       map[string]string{"app.kubernetes.io/name": "iris-mlp"},
		Selector:     map[string]string{"app.kubernetes.io/name": "iris-mlp"},
		MinAvailable: 2,
	}
	if err := cluster.ApplyPDB(pdb); err != nil {
		t.Fatalf("ApplyPDB: %v", err)
	}
	got, ok := cluster.GetPDB("default/iris-mlp")
	if !ok || got.MinAvailable != 2 {
		t.Fatalf("GetPDB = %+v (ok=%v), want minAvailable 2", got, ok)
	}
	if cluster.PDBCount() != 1 {
		t.Fatalf("PDBCount = %d, want 1", cluster.PDBCount())
	}
	pdb.MinAvailable = 1
	if err := cluster.ApplyPDB(pdb); err != nil {
		t.Fatalf("ApplyPDB update: %v", err)
	}
	got, _ = cluster.GetPDB("default/iris-mlp")
	if got.MinAvailable != 1 {
		t.Fatalf("updated PDB minAvailable = %d, want 1", got.MinAvailable)
	}

	snap := cluster.Snapshot()
	restored := NewFakeCluster()
	restored.Restore(snap)
	got, ok = restored.GetPDB("default/iris-mlp")
	if !ok || got.MinAvailable != 1 {
		t.Fatalf("restored PDB = %+v (ok=%v), want minAvailable 1", got, ok)
	}

	if err := cluster.DeletePDB("default/iris-mlp"); err != nil {
		t.Fatalf("DeletePDB: %v", err)
	}
	if _, ok := cluster.GetPDB("default/iris-mlp"); ok {
		t.Fatal("PDB survived DeletePDB")
	}
	if cluster.PDBCount() != 0 {
		t.Fatalf("PDBCount = %d, want 0", cluster.PDBCount())
	}
}
