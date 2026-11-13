package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdReady implements `mlop ready <name> --replicas N [--canary]`.
// The fake cluster has no pods, so nothing ever becomes ready on
// its own: this command is the CLI's way of simulating pods passing
// the readiness gate, the same role SetReady plays in tests and the
// demo. It reconciles afterwards, so `mlop status` shows the new
// phase immediately. --canary targets the canary Deployment
// (<name>-canary) instead of the stable one.
func cmdReady(opts *options, args []string) int {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop ready <name> --replicas N [--canary]\n")
	}
	replicas := fs.Int("replicas", -1, "how many pods pass the readiness gate")
	canary := fs.Bool("canary", false, "target the canary Deployment instead of the stable one")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop ready: exactly one deployment name is required")
		return 2
	}
	if *replicas < 0 {
		fmt.Fprintln(opts.stderr, "mlop ready: --replicas N is required (N >= 0)")
		return 2
	}
	key := resolveKey(fs.Arg(0))
	target := key
	if *canary {
		ns, name, _ := strings.Cut(key, "/")
		target = ns + "/" + name + "-canary"
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop ready: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop ready: no deployment %q\n", key)
		return 1
	}
	if _, ok := rt.Cluster.GetDeployment(target); !ok {
		hint := " (apply first)"
		if *canary {
			hint = " (the canary Deployment is created by a canary apply)"
		}
		fmt.Fprintf(opts.stderr, "mlop ready: no Deployment %q in the cluster yet%s\n", target, hint)
		return 1
	}
	rt.Cluster.SetReady(target, int32(*replicas))
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop ready: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop ready: %v\n", err)
		return 1
	}
	d := rt.Registry.Get(key).Deployment
	fmt.Fprintf(opts.stdout, "%s: %d pod(s) ready for %s — phase=%s (%d/%d ready)\n",
		key, *replicas, target, res.Phase, d.Status.ReadyReplicas, desiredReplicas(d))
	return 0
}
