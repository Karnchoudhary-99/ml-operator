package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	// The audit trail survives the round trip too: Progressing
	// after the first reconcile, Available after the pods came up.
	hist := rt.Reconciler.History("default/iris-mlp")
	if len(hist) != 2 {
		t.Fatalf("restored history = %d entries, want 2", len(hist))
	}
	if hist[0].Phase != api.PhaseProgressing || hist[1].Phase != api.PhaseAvailable {
		t.Fatalf("restored history phases = %s/%s, want Progressing/Available", hist[0].Phase, hist[1].Phase)
	}
	if hist[1].Image != "registry.example.com/iris-mlp:v1" || hist[1].Version != "v1" {
		t.Fatalf("restored history entry = %+v, want v1 image/version", hist[1])
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

// TestRoundTripBlueGreenState drives a blue-green rollout to the
// switched state, saves, and checks the restored runtime keeps the
// rollout where it was — the fold must happen on the next
// reconcile, not restart the soak.
func TestRoundTripBlueGreenState(t *testing.T) {
	reg := registry.New()
	reg.Register(dep())
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)

	// v2 under BlueGreen: create green, mark it ready, switch.
	replicas := int32(2)
	v2 := dep()
	v2.Generation = 2
	v2.Spec.Model = api.ModelSpec{Image: "registry.example.com/iris-mlp:v2", Framework: "onnx", Version: "v2"}
	v2.Spec.Strategy = api.StrategyBlueGreen
	v2.Spec.Replicas = &replicas
	reg.Register(v2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp-green", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	svc, _ := cluster.GetService("default/iris-mlp")
	if svc.Selector["ml-operator/green"] != "true" {
		t.Fatalf("setup: service selector = %v, want green=true (switched)", svc.Selector)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, From(reg, cluster, rec)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rt := loaded.Restore()
	bs, ok := rt.Reconciler.BlueGreenState("default/iris-mlp")
	if !ok || !bs.Switched || bs.Done || bs.Generation != 2 {
		t.Fatalf("restored blue-green state = %+v/%v, want Switched generation 2", bs, ok)
	}
	// The restored runtime folds on the next reconcile: stable
	// takes v2, green is deleted, no re-soak.
	res, err := rt.Reconciler.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile after restore: %v", err)
	}
	if got, _ := rt.Cluster.GetDeployment("default/iris-mlp"); got.Container.Image != "registry.example.com/iris-mlp:v2" {
		t.Fatalf("stable image = %s, want v2 after the restored fold", got.Container.Image)
	}
	if _, ok := rt.Cluster.GetDeployment("default/iris-mlp-green"); ok {
		t.Fatal("green Deployment survived the restored fold")
	}
	if res.Phase != api.PhaseAvailable {
		t.Fatalf("phase=%s, want Available", res.Phase)
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

// TestRoundTripConditionTransitionTimes pins that condition
// transition times survive the state file: a deployment that became
// Available at a known clock time still reports that exact stamp
// (and reason) after save + load + restore.
func TestRoundTripConditionTransitionTimes(t *testing.T) {
	reg := registry.New()
	reg.Register(dep())
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	stamped := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	rec.Now = func() time.Time { return stamped }
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, From(reg, cluster, rec)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := loaded.Restore().Registry.Get("default/iris-mlp")
	if entry == nil {
		t.Fatalf("restored registry lost the deployment")
	}
	c := entry.Deployment.Status.ConditionFor("Ready")
	if c == nil || c.Status != "True" || c.Reason != "ReplicasAvailable" {
		t.Fatalf("restored Ready condition = %+v, want True/ReplicasAvailable", c)
	}
	if !c.LastTransitionTime.Equal(stamped) {
		t.Fatalf("restored Ready stamped %v, want %v", c.LastTransitionTime, stamped)
	}
}

// TestRoundTripImagePullSecrets pins that pull secrets survive
// a state round trip in both places that carry them: the
// registry's spec (so a later apply compares against the real
// credentials) and the restored cluster's rendered Deployment
// (so a restart never drops the credentials off the pod
// template and the next pull fails).
func TestRoundTripImagePullSecrets(t *testing.T) {
	d := dep()
	d.Spec.ImagePullSecrets = []string{"regcred", "ghcr-creds"}
	reg := registry.New()
	reg.Register(d)
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	path := filepath.Join(t.TempDir(), "state.json")
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
	if got := entry.Deployment.Spec.ImagePullSecrets; len(got) != 2 || got[0] != "regcred" || got[1] != "ghcr-creds" {
		t.Fatalf("restored spec ImagePullSecrets = %v", got)
	}
	live, ok := rt.Cluster.GetDeployment("default/iris-mlp")
	if !ok {
		t.Fatalf("restored cluster lost the Deployment")
	}
	if got := live.ImagePullSecrets; len(got) != 2 || got[0] != "regcred" || got[1] != "ghcr-creds" {
		t.Fatalf("restored cluster Deployment ImagePullSecrets = %v", got)
	}
	// The restored runtime reconciles in sync: the secrets
	// match, so nothing re-applies.
	res, err := rt.Reconciler.Reconcile("default/iris-mlp")
	if err != nil {
		t.Fatalf("Reconcile after restore: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != controller.ActionNoOp {
		t.Fatalf("reconcile after restore applied %v, want a single NoOp", res.Actions)
	}
}
