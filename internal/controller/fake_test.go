package controller

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

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
