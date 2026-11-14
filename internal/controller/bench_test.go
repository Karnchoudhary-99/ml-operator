package controller

import (
	"fmt"
	"testing"
)

// Benchmarks for the reconciler's hot paths: an in-sync reconcile
// (the steady-state cost of the level-triggered loop) and a Plan
// (the read-only cost behind `mlop diff`). They are smoke-level
// numbers for catching regressions, not production load tests — the
// operator reconciles a handful of deployments, not thousands.
func BenchmarkReconcileInSync(b *testing.B) {
	reg, cluster, rec := setup(b)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		b.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPlanInSync(b *testing.B) {
	reg, cluster, rec := setup(b)
	reg.Register(dep("iris-mlp", 1))
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		b.Fatal(err)
	}
	cluster.SetReady("default/iris-mlp", 2)
	if _, err := rec.Reconcile("default/iris-mlp"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rec.Plan("default/iris-mlp"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReconcileManyDeployments measures one full pass over 100
// in-sync deployments — the cost of a sweep when the registry grows.
func BenchmarkReconcileManyDeployments(b *testing.B) {
	reg, cluster, rec := setup(b)
	keys := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("model-%03d", i)
		reg.Register(dep(name, 1))
		key := "default/" + name
		keys = append(keys, key)
		if _, err := rec.Reconcile(key); err != nil {
			b.Fatal(err)
		}
		cluster.SetReady(key, 2)
		if _, err := rec.Reconcile(key); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, key := range keys {
			if _, err := rec.Reconcile(key); err != nil {
				b.Fatal(err)
			}
		}
	}
}
