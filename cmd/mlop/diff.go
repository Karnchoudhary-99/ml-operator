package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// diffView is the JSON form of a diff: the deployment, the generation
// the plan was computed for, whether live already matches desired,
// and the actions Reconcile would execute (a lone NoOp when in sync).
// Paused reports whether the deployment is paused: the plan is what
// Reconcile would do on resume, nothing applies while paused.
type diffView struct {
	Deployment string              `json:"deployment"`
	Generation int64               `json:"generation"`
	InSync     bool                `json:"inSync"`
	Paused     bool                `json:"paused"`
	Actions    []controller.Action `json:"actions"`
}

// cmdDiff implements `mlop diff <name>` and `mlop diff -f
// manifest.yaml [--json]`: show the apply plan Reconcile would
// execute, without changing any state. The state file is never
// written by diff — a -f manifest is registered only in memory,
// with the same generation rules as apply, so the plan reflects
// exactly what a real apply would do next. Use it to review a
// manifest change (or a stuck deployment) before applying it.
func cmdDiff(opts *options, args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop diff <name> | mlop diff -f manifest.yaml [--json]\n")
	}
	file := fs.String("f", "", "manifest file (YAML or JSON)")
	jsonOut := fs.Bool("json", false, "print the plan as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if (*file == "") == (fs.NArg() == 0) {
		fmt.Fprintln(opts.stderr, "mlop diff: give a deployment name or -f manifest.yaml (exactly one)")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop diff: %v\n", err)
		return 1
	}
	rt := st.Restore()

	var key string
	var generation int64
	if *file != "" {
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop diff: read %s: %v\n", *file, err)
			return 1
		}
		d, err := manifest.Parse(raw)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop diff: %v\n", err)
			return 1
		}
		api.ApplyDefaults(&d)
		if errs := api.Validate(&d); len(errs) > 0 {
			fmt.Fprintf(opts.stderr, "mlop diff: manifest is invalid:\n")
			for _, e := range errs {
				fmt.Fprintf(opts.stderr, "  - %s\n", e)
			}
			return 1
		}
		// Generation follows the same rules as apply: a new key
		// starts at 1, a changed spec bumps the existing
		// generation, an identical re-apply keeps it. The
		// registration lives only in memory — diff never saves.
		key = d.Key()
		generation = 1
		if existing := rt.Registry.Get(key); existing != nil {
			generation = existing.Deployment.Generation
			if !registry.SpecsEqual(existing.Deployment.Spec, d.Spec) {
				generation++
			}
		}
		d.Generation = generation
		rt.Registry.Register(d)
	} else {
		key = resolveKey(fs.Arg(0))
		entry := rt.Registry.Get(key)
		if entry == nil {
			fmt.Fprintf(opts.stderr, "mlop diff: no deployment %q\n", key)
			return 1
		}
		generation = entry.Deployment.Generation
	}

	actions, err := rt.Reconciler.Plan(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop diff: %v\n", err)
		return 1
	}
	// The plan is computed from the registered spec, so it shows
	// what Reconcile would do on resume even while paused — the
	// note below says so, instead of letting the plan look like
	// an imminent apply.
	paused := rt.Registry.Get(key).Deployment.Spec.Paused
	view := diffView{Deployment: key, Generation: generation, Actions: actions,
		InSync: len(actions) == 1 && actions[0].Kind == controller.ActionNoOp, Paused: paused}
	if *jsonOut {
		enc := json.NewEncoder(opts.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(view); err != nil {
			fmt.Fprintf(opts.stderr, "mlop diff: %v\n", err)
			return 1
		}
		return 0
	}
	if paused {
		fmt.Fprintf(opts.stdout, "note: %s is paused — this plan applies on resume, nothing changes now\n", key)
	}
	if view.InSync {
		fmt.Fprintf(opts.stdout, "in sync: %s (generation %d) — %s\n", key, generation, actions[0].Reason)
		return 0
	}
	fmt.Fprintf(opts.stdout, "diff %s (generation %d):\n", key, generation)
	for _, a := range actions {
		fmt.Fprintf(opts.stdout, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
	return 0
}
