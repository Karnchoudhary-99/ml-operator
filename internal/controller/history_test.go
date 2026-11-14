package controller

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// History is the reconciler's audit trail: one entry per real
// transition (generation, phase, or desired image change), never
// one per reconcile — a steady deployment must not spam its log.
func TestHistoryRecordsTransitions(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))

	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	h := rec.History("default/iris-mlp")
	if len(h) != 1 {
		t.Fatalf("history after first reconcile = %d entries, want 1", len(h))
	}
	if h[0].Generation != 1 || h[0].Version != "v1" || h[0].Phase != api.PhaseProgressing {
		t.Fatalf("first entry wrong: %+v", h[0])
	}

	// Pods ready: the phase flips, so a second entry is recorded.
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	h = rec.History("default/iris-mlp")
	if len(h) != 2 || h[1].Phase != api.PhaseAvailable {
		t.Fatalf("history after ready = %+v, want 2 entries ending Available", h)
	}

	// A steady-state reconcile (in sync, still Available) records
	// nothing new: the log is transitions, not noise.
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.History("default/iris-mlp")); got != 2 {
		t.Fatalf("steady reconcile added entries: %d, want 2", got)
	}

	// New generation, new image: a third entry, for gen 2 / v2.
	d := dep("iris-mlp", 2)
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d.Spec.Model.Version = "v2"
	reg.Register(d)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	h = rec.History("default/iris-mlp")
	if len(h) != 3 {
		t.Fatalf("history after v2 = %d entries, want 3", len(h))
	}
	if h[2].Generation != 2 || h[2].Image != "registry.example.com/iris-mlp:v2" || h[2].Version != "v2" {
		t.Fatalf("v2 entry wrong: %+v", h[2])
	}
}

func TestHistoryRestoreRoundTrip(t *testing.T) {
	reg, cluster, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	want := rec.History("default/iris-mlp")
	if len(want) != 2 {
		t.Fatalf("setup history = %d entries, want 2", len(want))
	}

	// A fresh reconciler over the same registry restores the trail.
	rec2 := New(reg, cluster)
	rec2.RestoreHistory("default/iris-mlp", want)
	got := rec2.History("default/iris-mlp")
	if len(got) != len(want) {
		t.Fatalf("restored %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d mismatch: %+v vs %+v", i, got[i], want[i])
		}
	}

	// A trail for a deployment that left the registry is dropped.
	rec2.RestoreHistory("default/gone", want)
	if got := rec2.History("default/gone"); len(got) != 0 {
		t.Fatalf("restored history for unknown deployment: %d entries", len(got))
	}
}

func TestHistoryDroppedOnDelete(t *testing.T) {
	reg, _, rec := setup(t)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.History("default/iris-mlp")); got != 1 {
		t.Fatalf("setup history = %d entries, want 1", got)
	}
	reg.Delete("default/iris-mlp")
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.History("default/iris-mlp")); got != 0 {
		t.Fatalf("history survived deletion: %d entries, want 0", got)
	}
}
