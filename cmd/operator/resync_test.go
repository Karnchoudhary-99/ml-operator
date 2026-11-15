package main

import (
	"context"
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/config"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

func resyncDep(name string) api.ModelDeployment {
	replicas := int32(1)
	return api.ModelDeployment{
		Name:       name,
		Namespace:  "default",
		Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/" + name + ":v1", Framework: "onnx", Version: "v1"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyRolling,
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
}

func TestResyncAllReconcilesEveryDeployment(t *testing.T) {
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	reg.Register(resyncDep("iris-mlp"))
	reg.Register(resyncDep("bert-base"))

	if n := resyncAll(reg, rec); n != 2 {
		t.Fatalf("resyncAll reconciled %d, want 2", n)
	}
	if cluster.DeploymentCount() != 2 {
		t.Errorf("cluster has %d Deployments, want 2", cluster.DeploymentCount())
	}
	// A second resync is a no-op for steady deployments: nothing is
	// re-created, and the count stays the same.
	if n := resyncAll(reg, rec); n != 2 {
		t.Fatalf("second resyncAll reconciled %d, want 2", n)
	}
	if cluster.DeploymentCount() != 2 {
		t.Errorf("cluster has %d Deployments after second resync, want 2", cluster.DeploymentCount())
	}
}

func TestResyncAllEmptyRegistry(t *testing.T) {
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	if n := resyncAll(reg, rec); n != 0 {
		t.Errorf("resyncAll on an empty registry returned %d, want 0", n)
	}
}

func TestResyncLoopStopsOnCancel(t *testing.T) {
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	reg.Register(resyncDep("iris-mlp"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		resyncLoop(ctx, reg, rec, 10*time.Millisecond)
		close(done)
	}()
	// Let at least one tick fire, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resyncLoop did not stop after context cancel")
	}
	if cluster.DeploymentCount() != 1 {
		t.Errorf("resync loop created %d Deployments, want 1", cluster.DeploymentCount())
	}
}

func TestApplyConfigToReconciler(t *testing.T) {
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	cfg := config.Defaults()
	cfg.ApplyMaxAttempts = 7
	cfg.ApplyRetryBackoffMs = 250
	applyConfigToReconciler(cfg, rec)
	if rec.ApplyMaxAttempts != 7 {
		t.Errorf("ApplyMaxAttempts = %d, want 7", rec.ApplyMaxAttempts)
	}
	if rec.ApplyRetryBackoff != 250*time.Millisecond {
		t.Errorf("ApplyRetryBackoff = %v, want 250ms", rec.ApplyRetryBackoff)
	}
}
