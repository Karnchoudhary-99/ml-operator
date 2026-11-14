package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdLoad implements `mlop load <name> [--canary | --green] [--loading | --fail]`.
// The fake cluster has no serving containers, so the model-load
// probe never reports on its own: this command is the CLI's way of
// simulating the probe, the same role SetModelLoading /
// SetModelLoaded / SetModelLoadFailed play in tests and the demo.
// By default it reports the model loaded; --loading reports it
// still loading, --fail reports a failed load (bad artifact, OOM,
// missing weights). It reconciles afterwards, so a canary or
// blue-green gate sees the probe outcome immediately: a loading
// model holds the rollout without burning failure budget, a failed
// load burns budget and aborts it after three strikes. --canary
// targets the canary Deployment (<name>-canary) instead of the
// stable one; --green targets the green Deployment (<name>-green).
func cmdLoad(opts *options, args []string) int {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop load <name> [--canary | --green] [--loading | --fail]\n")
	}
	canary := fs.Bool("canary", false, "target the canary Deployment instead of the stable one")
	green := fs.Bool("green", false, "target the green Deployment instead of the stable one")
	loading := fs.Bool("loading", false, "report that the model is still loading")
	fail := fs.Bool("fail", false, "report that the model failed to load instead of loaded")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop load: exactly one deployment name is required")
		return 2
	}
	if *canary && *green {
		fmt.Fprintln(opts.stderr, "mlop load: --canary and --green are mutually exclusive")
		return 2
	}
	if *loading && *fail {
		fmt.Fprintln(opts.stderr, "mlop load: --loading and --fail are mutually exclusive")
		return 2
	}
	key := resolveKey(fs.Arg(0))
	target := key
	if *canary {
		ns, name, _ := strings.Cut(key, "/")
		target = ns + "/" + name + "-canary"
	}
	if *green {
		ns, name, _ := strings.Cut(key, "/")
		target = ns + "/" + name + "-green"
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop load: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop load: no deployment %q\n", key)
		return 1
	}
	if _, ok := rt.Cluster.GetDeployment(target); !ok {
		hint := " (apply first)"
		if *canary {
			hint = " (the canary Deployment is created by a canary apply)"
		}
		if *green {
			hint = " (the green Deployment is created by a blue-green apply)"
		}
		fmt.Fprintf(opts.stderr, "mlop load: no Deployment %q in the cluster yet%s\n", target, hint)
		return 1
	}
	outcome := "loaded"
	switch {
	case *fail:
		rt.Cluster.SetModelLoadFailed(target)
		outcome = "failed to load"
	case *loading:
		rt.Cluster.SetModelLoading(target)
		outcome = "still loading"
	default:
		rt.Cluster.SetModelLoaded(target)
	}
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop load: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop load: %v\n", err)
		return 1
	}
	d := rt.Registry.Get(key).Deployment
	fmt.Fprintf(opts.stdout, "%s: model %s for %s — phase=%s (%d/%d ready)\n",
		key, outcome, target, res.Phase, d.Status.ReadyReplicas, desiredReplicas(d))
	return 0
}
