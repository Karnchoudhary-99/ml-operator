package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdRollout implements `mlop rollout ...`. Only `status` exists so
// far: it reconciles first (so a step interval that elapsed since
// the last invocation actually advances the rollout) and then
// reports the canary rollout for one deployment — ladder, current
// step and weight, consecutive gate failures, and whether it
// promoted or aborted. A deployment with no canary rollout in
// flight reports that plainly instead of an empty table.
func cmdRollout(opts *options, args []string) int {
	if len(args) == 0 || args[0] != "status" {
		fmt.Fprint(opts.stderr, "Usage: mlop rollout status <name>\n")
		return 2
	}
	fs := flag.NewFlagSet("rollout status", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop rollout status <name>\n")
	}
	if err := fs.Parse(reorderFlags(fs, args[1:])); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop rollout status: exactly one deployment name is required")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollout: %v\n", err)
		return 1
	}
	rt := st.Restore()
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop rollout: no deployment %q\n", key)
		return 1
	}
	if _, err := rt.Reconciler.Reconcile(key); err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollout: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop rollout: %v\n", err)
		return 1
	}

	d := rt.Registry.Get(key).Deployment
	rs, ok := rt.Reconciler.RolloutState(key)
	if !ok {
		fmt.Fprintf(opts.stdout, "no canary rollout in flight for %s (strategy %s, phase %s)\n",
			key, d.Spec.Strategy, d.Status.Phase)
		return 0
	}
	fmt.Fprintf(opts.stdout, "rollout: %s\n", key)
	fmt.Fprintf(opts.stdout, "  model:      %s (%s)\n", d.Spec.Model.Image, d.Spec.Model.Version)
	fmt.Fprintf(opts.stdout, "  generation: %d\n", rs.Generation)
	fmt.Fprintf(opts.stdout, "  ladder:     %v\n", rs.Weights)
	fmt.Fprintf(opts.stdout, "  step:       %d/%d (weight %d%%)\n", rs.Step+1, len(rs.Weights), rs.CurrentWeight())
	fmt.Fprintf(opts.stdout, "  failures:   %d consecutive gate failures\n", rs.Failures)
	switch {
	case rs.Done && rs.Aborted:
		fmt.Fprintln(opts.stdout, "  state:      aborted — stable keeps serving the previous version")
	case rs.Done:
		fmt.Fprintln(opts.stdout, "  state:      promoted — stable now serves the new version")
	default:
		fmt.Fprintf(opts.stdout, "  state:      in progress (phase %s)\n", d.Status.Phase)
	}
	return 0
}
