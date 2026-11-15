package controller

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

// TestReconcileCreatesPDB pins the PDB lifecycle on first
// reconcile: a 2-replica deployment gets a PDB with minAvailable 1
// (the replicas-1 default), as the last action after the Service,
// and a second reconcile is a clean NoOp — the PDB does not flap.
func TestReconcileCreatesPDB(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 3 || res.Actions[2].Kind != ActionCreatePDB {
		t.Fatalf("actions = %v, want [CreateDeployment CreateService CreatePDB]", res.Actions)
	}
	pdb, ok := cluster.GetPDB("default/iris-mlp")
	if !ok {
		t.Fatal("PDB was not created")
	}
	if pdb.MinAvailable != 1 {
		t.Fatalf("PDB minAvailable = %d, want 1 (replicas-1 default)", pdb.MinAvailable)
	}
	if pdb.Selector["app.kubernetes.io/name"] != "iris-mlp" {
		t.Fatalf("PDB selector = %v, want the model labels", pdb.Selector)
	}

	cluster.SetReady("default/iris-mlp", 2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("second reconcile actions = %v, want a single NoOp", res.Actions)
	}
}

// TestReconcilePDBUpdate pins the PDB update path: scaling 2 -> 4
// replicas moves minAvailable 1 -> 3, and the reconciler reports
// an UpdatePDB instead of recreating anything.
func TestReconcilePDBUpdate(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}

	d := dep("iris-mlp", 2)
	replicas := int32(4)
	d.Spec.Replicas = &replicas
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 2 || res.Actions[1].Kind != ActionUpdatePDB {
		t.Fatalf("actions = %v, want [UpdateDeployment UpdatePDB]", res.Actions)
	}
	if res.Actions[1].Reason != "minAvailable 1 -> 3" {
		t.Fatalf("UpdatePDB reason = %q, want \"minAvailable 1 -> 3\"", res.Actions[1].Reason)
	}
	pdb, _ := cluster.GetPDB("default/iris-mlp")
	if pdb.MinAvailable != 3 {
		t.Fatalf("PDB minAvailable = %d, want 3", pdb.MinAvailable)
	}

	// An explicit minAvailable survives a re-reconcile unchanged.
	d.Spec.Availability = &api.AvailabilitySpec{MinAvailable: 2}
	d.Generation = 3
	reg.Register(d)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionUpdatePDB {
		t.Fatalf("actions = %v, want a single UpdatePDB", res.Actions)
	}
	pdb, _ = cluster.GetPDB("default/iris-mlp")
	if pdb.MinAvailable != 2 {
		t.Fatalf("PDB minAvailable = %d, want 2", pdb.MinAvailable)
	}
}

// TestReconcileDeletesPDBOnScaleToOne pins the PDB removal path:
// scaling down to a single replica leaves nothing to protect, so
// the leftover PDB is deleted and the plan goes back to NoOp.
func TestReconcileDeletesPDBOnScaleToOne(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if cluster.PDBCount() != 1 {
		t.Fatalf("setup: PDBCount = %d, want 1", cluster.PDBCount())
	}

	d := dep("iris-mlp", 2)
	one := int32(1)
	d.Spec.Replicas = &one
	reg.Register(d)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 2 || res.Actions[1].Kind != ActionDeletePDB {
		t.Fatalf("actions = %v, want [UpdateDeployment DeletePDB]", res.Actions)
	}
	if cluster.PDBCount() != 0 {
		t.Fatalf("PDBCount = %d, want 0", cluster.PDBCount())
	}
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActionNoOp {
		t.Fatalf("actions = %v, want a single NoOp", res.Actions)
	}
}

// TestPlanPDBIsReadOnly pins that `mlop diff` previews the PDB
// without creating it: a fresh plan lists CreatePDB, the cluster
// stays empty, and a second plan agrees.
func TestPlanPDBIsReadOnly(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	actions, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	found := false
	for _, a := range actions {
		if a.Kind == ActionCreatePDB {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan = %v, want a CreatePDB action", actions)
	}
	if cluster.PDBCount() != 0 {
		t.Fatalf("Plan created a PDB: count = %d", cluster.PDBCount())
	}
	again, err := rec.Plan("default/iris-mlp")
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if len(again) != len(actions) {
		t.Fatalf("second plan = %v, want %v", again, actions)
	}
}

// TestReconcilePDBRetry pins the retry policy on PDB writes: a
// transient apply failure on the PDB update is retried and
// recovered (one ApplyRetried event), not reported as Degraded.
func TestReconcilePDBRetry(t *testing.T) {
	reg, cluster, rec := setup(t)
	rec.ApplyRetryBackoff = 0 // instant retries in tests
	d := dep("iris-mlp", 1)
	three := int32(3)
	d.Spec.Replicas = &three
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	pdb, _ := cluster.GetPDB("default/iris-mlp")
	if pdb.MinAvailable != 2 {
		t.Fatalf("setup: PDB minAvailable = %d, want 2 (replicas-1 default)", pdb.MinAvailable)
	}

	// Only the PDB changes (same 3 replicas, explicit minAvailable
	// 1), so the plan is a single UpdatePDB: arm one failure and
	// the retry must land it.
	d2 := dep("iris-mlp", 2)
	d2.Spec.Replicas = &three
	d2.Spec.Availability = &api.AvailabilitySpec{MinAvailable: 1}
	reg.Register(d2)
	cluster.FailNextApplies(1)
	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	pdb, ok := cluster.GetPDB("default/iris-mlp")
	if !ok || pdb.MinAvailable != 1 {
		t.Fatalf("PDB = %+v (ok=%v), want minAvailable 1 after retried update", pdb, ok)
	}
	if res.Phase != api.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing (not Degraded)", res.Phase)
	}
	if got := rec.Metrics.Counter(metrics.MetricApplyRetriesTotal, "outcome", "recovered"); got != 1 {
		t.Fatalf("recovered retries = %v, want 1", got)
	}
	found := false
	for _, e := range rec.Events.EventsFor("default/iris-mlp") {
		if e.Reason == "ApplyRetried" && strings.Contains(e.Message, "UpdatePDB") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ApplyRetried event for the PDB update: %v", rec.Events.EventsFor("default/iris-mlp"))
	}
}
