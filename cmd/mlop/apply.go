package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdApply implements `mlop apply -f manifest.yaml`: parse the
// manifest, apply defaults, validate, register it (bumping the
// generation when the spec changed), reconcile once against the
// simulated cluster, and persist the new state. --dry-run stops
// after validation and prints what would be applied.
func cmdApply(opts *options, args []string) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop apply -f manifest.yaml [--dry-run]\n")
	}
	file := fs.String("f", "", "manifest file (YAML or JSON)")
	dryRun := fs.Bool("dry-run", false, "validate only, do not change state")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(opts.stderr, "mlop apply: -f manifest.yaml is required")
		return 2
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: read %s: %v\n", *file, err)
		return 1
	}
	d, err := manifest.Parse(raw)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	api.ApplyDefaults(&d)
	if errs := api.Validate(&d); len(errs) > 0 {
		fmt.Fprintf(opts.stderr, "mlop apply: manifest is invalid:\n")
		for _, e := range errs {
			fmt.Fprintf(opts.stderr, "  - %s\n", e)
		}
		return 1
	}
	if *dryRun {
		fmt.Fprintf(opts.stdout, "valid: %s (image %s, %s, %d replica(s))\n",
			d.Key(), d.Spec.Model.Image, d.Spec.Strategy, *d.Spec.Replicas)
		return 0
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	rt := st.Restore()

	// Generation is the CLI's job, never the manifest's: a new key
	// starts at 1, a changed spec bumps the existing generation, an
	// identical re-apply keeps it (the registry treats it as a
	// no-op, so nothing is re-rolled).
	key := d.Key()
	changed := true
	if existing := rt.Registry.Get(key); existing != nil {
		d.Generation = existing.Deployment.Generation
		if registry.SpecsEqual(existing.Deployment.Spec, d.Spec) {
			changed = false
		} else {
			d.Generation++
		}
	} else {
		d.Generation = 1
	}
	rt.Registry.Register(d)

	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	if changed {
		fmt.Fprintf(opts.stdout, "applied %s (generation %d): phase=%s\n", key, d.Generation, res.Phase)
	} else {
		fmt.Fprintf(opts.stdout, "unchanged %s (generation %d): phase=%s\n", key, d.Generation, res.Phase)
	}
	for _, a := range res.Actions {
		fmt.Fprintf(opts.stdout, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
	if res.Message != "" {
		fmt.Fprintf(opts.stdout, "  %s\n", res.Message)
	}
	return 0
}
