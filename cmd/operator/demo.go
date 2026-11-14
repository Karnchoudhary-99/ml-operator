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
// model-load probe, promotion). It prints every action and phase,
// so the reconcile loop is visible without a real cluster.
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
	// the 30s canary interval. The model-load probe is part of the
	// story: the canary's pods are ready but its model is still
	// loading, so the first gate holds at 10% without burning
	// failure budget; only once the model loads does the rollout
	// advance.
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
	cluster.SetModelLoading("default/iris-mlp-canary")
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 6 (canary model still loading — hold at 10%)", res)

	// The model finishes loading; the gate passes and the rollout
	// advances on the next interval.
	cluster.SetModelLoaded("default/iris-mlp-canary")
	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 7 (canary 55%)", res)

	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 8 (canary 100%)", res)

	cluster.SetReady("default/iris-mlp-canary", 2)
	now = now.Add(31 * time.Second)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 9 (canary promoted)", res)

	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 10 (in sync)", res)

	// Autoscaling: the canary chapter is over, so the deployment
	// goes back to RollingUpdate and gains an HPA (min 1, max 5,
	// CPU 70%). With no metrics yet the scaler holds the current
	// replica count — unknown load never scales.
	d.Generation = 4
	d.Spec.Strategy = api.StrategyRolling
	d.Spec.Canary = nil
	d.Spec.Autoscaling = &api.AutoscalingSpec{MinReplicas: 1, MaxReplicas: 5, TargetCPUUtilization: 70}
	reg.Register(d)
	fmt.Fprintln(w, "registered iris-mlp autoscaling (min 1, max 5, cpu 70%)")
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 11 (autoscaling on — HPA created)", res)

	// A load spike (140% CPU vs 70% target) doubles the replicas.
	cluster.SetMetrics("default/iris-mlp", controller.ClusterMetrics{CPUUtilizationPercent: 140, RequestsPerSecond: -1, ErrorRate: -1})
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 12 (load spike — scale up)", res)

	// The spike is over (no metrics sample): the new pods come up
	// and the deployment is Available at 4 replicas.
	cluster.SetMetrics("default/iris-mlp", controller.ClusterMetrics{CPUUtilizationPercent: -1, RequestsPerSecond: -1, ErrorRate: -1})
	cluster.SetReady("default/iris-mlp", 4)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 13 (4 replicas ready)", res)

	// The load dips (10% CPU): the first scale-down applies at
	// once, back to 1 replica.
	cluster.SetMetrics("default/iris-mlp", controller.ClusterMetrics{CPUUtilizationPercent: 10, RequestsPerSecond: -1, ErrorRate: -1})
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 14 (load dip — scale down)", res)

	cluster.SetReady("default/iris-mlp", 1)
	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 15 (1 replica ready)", res)

	// Observability: the metrics the reconciler recorded, in
	// Prometheus exposition format, and the events it raised.
	fmt.Fprintln(w, "--- metrics ---")
	fmt.Fprint(w, rec.Metrics.Render())
	fmt.Fprintln(w, "--- events ---")
	for _, e := range rec.Events.List() {
		fmt.Fprintf(w, "  - %s %s %s: %s (x%d)\n", e.Type, e.Reason, e.Deployment, e.Message, e.Count)
	}

	res, err = rec.Reconcile("default/iris-mlp")
	if err != nil {
		return err
	}
	printResult(w, "reconcile 16 (in sync)", res)
	return nil
}

func printResult(w io.Writer, label string, res controller.Result) {
	fmt.Fprintf(w, "%s: phase=%s requeue=%v message=%q\n", label, res.Phase, res.Requeue, res.Message)
	for _, a := range res.Actions {
		fmt.Fprintf(w, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
}
