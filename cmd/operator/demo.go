package main

import (
	"fmt"
	"io"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
)

// runDemo exercises the reconciler end to end against the in-memory
// fake cluster: register -> reconcile (create) -> pods ready ->
// reconcile (available) -> new image -> reconcile (rolling update)
// -> canary rollout to a third version (weight steps, health gate,
// promotion). It prints every action and phase, so the reconcile
// loop is visible without a real cluster.
func runDemo(w io.Writer) error {
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)

	replicas := int32(2)
	d := api.ModelDeployment{
		Name:       "iris-mlp",
		Namespace:  "default",
		Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyRolling,
			Health:    api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
	reg.Register(d)
	fmt.Fprintln(w, "registered iris-mlp v1 (2 replicas)")

	res, err := rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 1 (create)", res)

	// Pods come up; the next reconcile reports Available.
	cluster.SetReady("default/iris-mlp", 2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 2 (pods ready)", res)

	// New model version: a rolling update in place.
	d.Generation = 2
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v2"
	d.Spec.Model.Version = "v2"
	reg.Register(d)
	fmt.Fprintln(w, "registered iris-mlp v2")
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 3 (rolling update to v2)", res)

	cluster.SetReady("default/iris-mlp", 2)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 4 (v2 ready)", res)

	// Canary rollout to v3: the stable Deployment keeps serving v2
	// while the canary steps 10% -> 55% -> 100% behind the health
	// gate, then promotes and the canary is deleted. The demo drives
	// the reconciler's clock 31s per step so each step waits out
	// the 30s canary interval.
	now := time.Now()
	rec.Now = func() time.Time { return now }
	d.Generation = 3
	d.Spec.Model.Image = "registry.example.com/iris-mlp:v3"
	d.Spec.Model.Version = "v3"
	d.Spec.Strategy = api.StrategyCanary
	d.Spec.Canary = &api.CanarySpec{Weight: 10, Steps: 2, IntervalSeconds: 30}
	reg.Register(d)
	fmt.Fprintln(w, "registered iris-mlp v3 (canary 10%, 2 steps)")
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 5 (canary v3 created)", res)

	cluster.SetReady("default/iris-mlp-canary", 1)
	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 6 (canary 55%)", res)

	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 7 (canary 100%)", res)

	cluster.SetReady("default/iris-mlp-canary", 2)
	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 8 (canary promoted)", res)

	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 9 (in sync)", res)
	return nil
}

func printResult(w io.Writer, label string, res controller.Result) {
	fmt.Fprintf(w, "%s: phase=%s requeue=%v message=%q\n", label, res.Phase, res.Requeue, res.Message)
	for _, a := range res.Actions {
		fmt.Fprintf(w, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
}
