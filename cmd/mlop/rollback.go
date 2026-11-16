package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdRollback implements `mlop rollback <name>`: restore the image
// and version the status remembers as previous — the image the
// stable Deployment served before the current one, recorded by the
// reconciler on the last image change (canary promotion or direct
// rolling/recreate update). The rollback is a new generation of the
// same deployment, so it rolls out through the deployment's normal
// strategy: a canary rollback is itself canary-gated, and the
// version being rolled back from becomes the new previous image,
// so a second rollback returns to it.
func cmdRollback(opts *options, args []string) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop rollback <name>\n")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop rollback: exactly one deployment name is required")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollback: %v\n", err)
		return 1
	}
	rt := st.Restore()
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop rollback: no deployment %q\n", key)
		return 1
	}
	d := entry.Deployment
	prevImage := d.Status.PreviousImage
	if prevImage == "" {
		fmt.Fprintf(opts.stderr, "mlop rollback: no previous version to roll back to for %s — its image has never been replaced\n", key)
		return 1
	}
	// A rollback is a new generation that must roll out through
	// the normal strategy; a paused reconciler would silently
	// swallow it, so refuse instead of pretending it happened.
	if d.Spec.Paused {
		fmt.Fprintf(opts.stderr, "mlop rollback: %s is paused; resume it first (mlop resume %s)\n", key, d.Name)
		return 1
	}
	if prevImage == d.Spec.Model.Image {
		fmt.Fprintf(opts.stderr, "mlop rollback: %s already serves the previous version %s (%s)\n", key, prevImage, d.Status.PreviousVersion)
		return 1
	}
	prevVersion := d.Status.PreviousVersion

	d.Spec.Model.Image = prevImage
	d.Spec.Model.Version = prevVersion
	d.Generation++
	rt.Registry.Register(d)

	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollback: reconcile %s: %v\n", key, err)
		return 1
	}
	if rt.Reconciler.Events != nil {
		rt.Reconciler.Events.Record(key, metrics.EventNormal, "RolledBack",
			fmt.Sprintf("rolled back to %s (%s)", prevImage, prevVersion))
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollback: %v\n", err)
		return 1
	}
	fmt.Fprintf(opts.stdout, "rolled back %s (generation %d): now serving %s (%s), phase=%s\n",
		key, d.Generation, prevImage, prevVersion, res.Phase)
	for _, a := range res.Actions {
		fmt.Fprintf(opts.stdout, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
	if res.Message != "" {
		fmt.Fprintf(opts.stdout, "  %s\n", res.Message)
	}
	return 0
}
