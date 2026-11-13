package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

func dep() api.ModelDeployment {
	replicas := int32(2)
	return api.ModelDeployment{
		Name: "iris-mlp", Namespace: "default", Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyRolling,
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	reg := registry.New()
	reg.Register(dep())
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec.Events.Record("default/iris-mlp", metrics.EventNormal, "Available", "deployment is available")

	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if err := Save(path, From(reg, cluster, rec)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rt := loaded.Restore()
	entry := rt.Registry.Get("default/iris-mlp")
	if entry == nil {
		t.Fatalf("restored registry lost the deployment")
	}
	if entry.Deployment.Status.Phase != api.PhaseAvailable {
		t.Fatalf("restored status phase = %s, want Available", entry.Deployment.Status.Phase)
	}
	if entry.Deployment.Status.ReadyReplicas != 2 {
		t.Fatalf("restored ready = %d, want 2", entry.Deployment.Status.ReadyReplicas)
	}
	if _, ok := rt.Cluster.GetDeployment("default/iris-mlp"); !ok {
		t.Fatalf("restored cluster lost the Deployment")
	}
	if rt.Cluster.ReadyReplicas("default/iris-mlp") != 2 {
		t.Fatalf("restored cluster ready = %d, want 2", rt.Cluster.ReadyReplicas("default/iris-mlp"))
	}
	events := rt.Reconciler.Events.EventsFor("default/iris-mlp")
	if len(events) == 0 {
		t.Fatalf("restored events are empty")
	}
	found := false
	for _, e := range events {
		if e.Reason == "Available" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored events lost the Available event: %v", events)
	}

	// The restored runtime reconciles in sync: nothing to apply.
	res, err := rt.Reconciler.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile after restore: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != controller.ActionNoOp {
		t.Fatalf("reconcile after restore applied %v, want a single NoOp", res.Actions)
	}
}

func TestLoadMissingFile(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope", "state.json"))
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if s == nil || len(s.Deployments) != 0 {
		t.Fatalf("missing file should load an empty state, got %+v", s)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected an error for a corrupt state file")
	}
}

func TestLoadFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version": 99, "deployments": []}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected an error for a future state version")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{Version: Version}
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// No temp files left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("state dir holds %v, want only state.json", entries)
	}
}

func TestDefaultPathEnvOverride(t *testing.T) {
	t.Setenv("MLOP_STATE", "/tmp/from-env/state.json")
	if got := DefaultPath(); got != "/tmp/from-env/state.json" {
		t.Fatalf("DefaultPath = %q, want the MLOP_STATE value", got)
	}
}
