package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdPause implements `mlop pause <name>`: suspend reconciliation
// for one deployment. The operator stops making cluster changes,
// advancing rollouts, and applying autoscaling decisions — the live
// objects keep serving exactly as they are — until `mlop resume`
// runs. Pausing is a spec change, so it bumps the generation; the
// reconcile that follows stamps the Paused=True condition and
// records the pause in the audit trail. Pausing an already-paused
// deployment is a no-op (exit 0, no generation bump, no reconcile).
func cmdPause(opts *options, args []string) int {
	return cmdSetPaused(opts, args, true, "pause")
}

// cmdResume implements `mlop resume <name>`: clear spec.paused and
// reconcile immediately, so any spec change that landed while the
// deployment was paused applies on this invocation. Resuming a
// deployment that is not paused is a no-op (exit 0).
func cmdResume(opts *options, args []string) int {
	return cmdSetPaused(opts, args, false, "resume")
}

func cmdSetPaused(opts *options, args []string, paused bool, verb string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprintf(opts.stderr, "Usage: mlop %s <name>\n", verb)
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(opts.stderr, "mlop %s: exactly one deployment name is required\n", verb)
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop %s: %v\n", verb, err)
		return 1
	}
	rt := st.Restore()
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop %s: no deployment %q\n", verb, key)
		return 1
	}
	d := entry.Deployment
	if d.Spec.Paused == paused {
		if paused {
			fmt.Fprintf(opts.stdout, "%s is already paused (generation %d)\n", key, d.Generation)
		} else {
			fmt.Fprintf(opts.stdout, "%s is not paused (generation %d)\n", key, d.Generation)
		}
		return 0
	}
	d.Spec.Paused = paused
	d.Generation++
	rt.Registry.Register(d)
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop %s: reconcile %s: %v\n", verb, key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop %s: %v\n", verb, err)
		return 1
	}
	if paused {
		fmt.Fprintf(opts.stdout, "paused %s (generation %d): phase=%s — reconciliation suspended, live objects keep serving\n",
			key, d.Generation, res.Phase)
	} else {
		fmt.Fprintf(opts.stdout, "resumed %s (generation %d): phase=%s\n", key, d.Generation, res.Phase)
	}
	return 0
}
