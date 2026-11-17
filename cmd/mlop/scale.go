package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdScale implements `mlop scale <name> --replicas N`: set the
// desired replica count for one deployment. It is a spec change, so
// it bumps the generation and reconciles immediately — the
// Deployment is updated in place, no rollout is needed for a pure
// replica change.
//
// Two refusals keep the command honest. When autoscaling is
// enabled the autoscaler owns the replica count: the reconciler
// overwrites spec.replicas with the scaler's decision on every
// reconcile, so a manual scale would be silently reverted on the
// next resync — scale refuses and points at the autoscaling
// bounds instead. And a paused deployment swallows the change the
// same way rollback refuses it: resume first.
//
// Scaling to 0 is allowed (the deployment stops serving) —
// validate only requires >= 0, and lint will warn about it on the
// next validate — but a no-op scale (already at N) exits 0 without
// a generation bump, like pause/resume.
func cmdScale(opts *options, args []string) int {
	fs := flag.NewFlagSet("scale", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop scale <name> --replicas N [--dry-run]\n")
	}
	replicas := fs.Int("replicas", -1, "desired replica count (>= 0)")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing state")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop scale: exactly one deployment name is required")
		return 2
	}
	if *replicas < 0 {
		fmt.Fprintln(opts.stderr, "mlop scale: --replicas N is required and must be >= 0")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop scale: %v\n", err)
		return 1
	}
	rt := st.Restore()
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop scale: no deployment %q\n", key)
		return 1
	}
	d := entry.Deployment
	if d.Spec.Autoscaling != nil {
		fmt.Fprintf(opts.stderr, "mlop scale: %s has autoscaling enabled (min %d, max %d) — the autoscaler owns replicas, so a manual scale would be reverted on the next reconcile; change spec.autoscaling.minReplicas/maxReplicas instead\n",
			key, d.Spec.Autoscaling.MinReplicas, d.Spec.Autoscaling.MaxReplicas)
		return 1
	}
	// A scale is a new generation that must reconcile; a paused
	// reconciler would silently swallow it, so refuse instead of
	// pretending it happened.
	if d.Spec.Paused {
		fmt.Fprintf(opts.stderr, "mlop scale: %s is paused; resume it first (mlop resume %s)\n", key, d.Name)
		return 1
	}
	current := api.DefaultReplicas
	if d.Spec.Replicas != nil {
		current = *d.Spec.Replicas
	}
	want := int32(*replicas)
	if want == current {
		fmt.Fprintf(opts.stdout, "%s is already at %d replicas (generation %d)\n", key, want, d.Generation)
		return 0
	}
	if *dryRun {
		fmt.Fprintf(opts.stdout, "would scale %s from %d to %d replicas (generation %d -> %d)\n",
			key, current, want, d.Generation, d.Generation+1)
		return 0
	}

	d.Spec.Replicas = &want
	d.Generation++
	rt.Registry.Register(d)

	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop scale: reconcile %s: %v\n", key, err)
		return 1
	}
	// The reconciler only records ScaledUp/ScaledDown for
	// autoscaler decisions; a manual scale gets its own event so
	// `mlop logs` shows who changed the replica count.
	if rt.Reconciler.Events != nil {
		rt.Reconciler.Events.Record(key, metrics.EventNormal, "Scaled",
			fmt.Sprintf("scaled from %d to %d replicas", current, want))
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop scale: %v\n", err)
		return 1
	}
	fmt.Fprintf(opts.stdout, "scaled %s (generation %d): %d -> %d replicas, phase=%s\n",
		key, d.Generation, current, want, res.Phase)
	for _, a := range res.Actions {
		fmt.Fprintf(opts.stdout, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
	if res.Message != "" {
		fmt.Fprintf(opts.stdout, "  %s\n", res.Message)
	}
	return 0
}
