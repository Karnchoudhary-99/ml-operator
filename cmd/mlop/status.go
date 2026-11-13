package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdStatus implements `mlop status [name]`: reconcile first (the
// operator is level-triggered, so this is a no-op when already in
// sync), then show the deployment phases. With no name it lists
// every deployment; with a name it shows the full detail view.
// A bare name means the "default" namespace; "ns/name" is accepted.
func cmdStatus(opts *options, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop status [name]\n")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(opts.stderr, "mlop status: at most one name")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
		return 1
	}
	rt := st.Restore()

	if fs.NArg() == 0 {
		entries := rt.Registry.List()
		if len(entries) == 0 {
			fmt.Fprintln(opts.stdout, "no deployments")
			return 0
		}
		for _, e := range entries {
			if _, err := rt.Reconciler.Reconcile(e.Deployment.Key()); err != nil {
				fmt.Fprintf(opts.stderr, "mlop status: reconcile %s: %v\n", e.Deployment.Key(), err)
				return 1
			}
		}
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
			return 1
		}
		fmt.Fprintln(opts.stdout, "NAME\tNAMESPACE\tPHASE\tREADY\tVERSION\tMESSAGE")
		for _, e := range rt.Registry.List() {
			d := e.Deployment
			fmt.Fprintf(opts.stdout, "%s\t%s\t%s\t%d/%d\t%s\t%s\n",
				d.Name, namespaceOf(d), d.Status.Phase,
				d.Status.ReadyReplicas, desiredReplicas(d),
				d.Spec.Model.Version, d.Status.Message)
		}
		return 0
	}

	key := resolveKey(fs.Arg(0))
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop status: no deployment %q\n", key)
		return 1
	}
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
		return 1
	}
	d := rt.Registry.Get(key).Deployment
	fmt.Fprintf(opts.stdout, "name:       %s\n", d.Name)
	fmt.Fprintf(opts.stdout, "namespace:  %s\n", namespaceOf(d))
	fmt.Fprintf(opts.stdout, "generation: %d (observed %d)\n", d.Generation, d.Status.ObservedGeneration)
	fmt.Fprintf(opts.stdout, "phase:      %s\n", d.Status.Phase)
	fmt.Fprintf(opts.stdout, "ready:      %d/%d replicas\n", d.Status.ReadyReplicas, desiredReplicas(d))
	fmt.Fprintf(opts.stdout, "model:      %s (%s, %s)\n", d.Spec.Model.Image, d.Spec.Model.Framework, d.Spec.Model.Version)
	fmt.Fprintf(opts.stdout, "strategy:   %s\n", d.Spec.Strategy)
	if d.Spec.Autoscaling != nil {
		fmt.Fprintf(opts.stdout, "autoscaling: min %d, max %d, cpu %d%%, rps %d\n",
			d.Spec.Autoscaling.MinReplicas, d.Spec.Autoscaling.MaxReplicas,
			d.Spec.Autoscaling.TargetCPUUtilization, d.Spec.Autoscaling.TargetRequestsPerSecond)
	}
	fmt.Fprintf(opts.stdout, "message:    %s\n", d.Status.Message)
	if len(d.Status.Conditions) > 0 {
		fmt.Fprintln(opts.stdout, "conditions:")
		for _, c := range d.Status.Conditions {
			fmt.Fprintf(opts.stdout, "  %s: %s (%s) %s\n", c.Type, c.Status, c.Reason, c.Message)
		}
	}
	if res.Requeue {
		fmt.Fprintln(opts.stdout, "requeue:    yes — still converging")
	}
	return 0
}

// resolveKey turns a CLI name into a registry key: "name" means
// "default/name", "ns/name" is used as-is.
func resolveKey(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "default/" + name
}

func namespaceOf(d api.ModelDeployment) string {
	if d.Namespace == "" {
		return "default"
	}
	return d.Namespace
}

func desiredReplicas(d api.ModelDeployment) int32 {
	if d.Spec.Replicas == nil {
		return api.DefaultReplicas
	}
	return *d.Spec.Replicas
}
