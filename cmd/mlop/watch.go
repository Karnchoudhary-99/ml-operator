package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdWatch implements `mlop watch <name>`: poll the deployment,
// reconciling on every tick, and print its phase until it reaches
// a terminal state. Exit 0 when it becomes Available, 1 when it
// turns Failed or Degraded, 2 when the timeout expires first. The
// fake cluster has no pods, so a deployment only becomes ready when
// `mlop ready` is run in another shell — watch is the "wait for the
// rollout" half of that pair.
func cmdWatch(opts *options, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop watch <name>\n")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop watch: exactly one deployment name is required")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop watch: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop watch: no deployment %q\n", key)
		return 1
	}

	// Poll every second for up to five minutes: long enough for a
	// canary step to advance, short enough to fail loudly instead
	// of hanging a script forever.
	interval := time.Second
	deadline := time.Now().Add(5 * time.Minute)
	for {
		res, err := rt.Reconciler.Reconcile(key)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop watch: reconcile %s: %v\n", key, err)
			return 1
		}
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop watch: %v\n", err)
			return 1
		}
		d := rt.Registry.Get(key).Deployment
		fmt.Fprintf(opts.stdout, "%s %s: %s %d/%d ready\n",
			time.Now().Format("15:04:05"), key, res.Phase,
			d.Status.ReadyReplicas, desiredReplicas(d))
		switch res.Phase {
		case api.PhaseAvailable:
			return 0
		case api.PhaseFailed, api.PhaseDegraded:
			return 1
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(opts.stderr, "mlop watch: timed out after 5m0s waiting for %s\n", key)
			return 2
		}
		time.Sleep(interval)
	}
}
