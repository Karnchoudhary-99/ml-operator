package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdTraffic implements `mlop traffic <name> [--canary | --green]
// --requests N [--errors E] [--p95-ms M]`.
//
// The fake cluster has no serving containers, so the health gate's
// error-rate and p95-latency checks would stay readiness-only
// forever: nothing ever reports traffic. This command is the CLI's
// way of simulating serving traffic, the same role SetModelLoaded /
// SetModelLoading / SetModelLoadFailed play for the model-load
// probe in `mlop load`. One invocation is one observation batch:
// --requests served, --errors of them failed, p95 latency
// --p95-ms. Batches accumulate in the cluster metrics (see
// FakeCluster.SetTraffic), so the gate judges the cumulative error
// rate — a bad deploy keeps failing the gate as its errors pile
// up, and a later healthy batch dilutes the rate back down.
// --canary targets the canary Deployment (<name>-canary) instead
// of the stable one; --green targets the green Deployment
// (<name>-green). It reconciles afterwards, so a canary or
// blue-green gate sees the traffic immediately: an error rate over
// the gate's threshold fails the gate, and three consecutive
// failures abort the rollout.
func cmdTraffic(opts *options, args []string) int {
	fs := flag.NewFlagSet("traffic", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop traffic <name> [--canary | --green] --requests N [--errors E] [--p95-ms M]\n")
	}
	canary := fs.Bool("canary", false, "target the canary Deployment instead of the stable one")
	green := fs.Bool("green", false, "target the green Deployment instead of the stable one")
	requests := fs.Int64("requests", 0, "requests served in this batch (required, > 0)")
	errors := fs.Int64("errors", 0, "failed requests in this batch (0 <= errors <= requests)")
	p95Ms := fs.Float64("p95-ms", 0, "p95 request latency in milliseconds for this batch (0 = not reported)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop traffic: exactly one deployment name is required")
		return 2
	}
	if *canary && *green {
		fmt.Fprintln(opts.stderr, "mlop traffic: --canary and --green are mutually exclusive")
		return 2
	}
	if *requests <= 0 {
		fmt.Fprintln(opts.stderr, "mlop traffic: --requests must be > 0")
		return 2
	}
	if *errors < 0 || *errors > *requests {
		fmt.Fprintln(opts.stderr, "mlop traffic: --errors must be between 0 and --requests")
		return 2
	}
	if *p95Ms < 0 {
		fmt.Fprintln(opts.stderr, "mlop traffic: --p95-ms must be >= 0")
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
		fmt.Fprintf(opts.stderr, "mlop traffic: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop traffic: no deployment %q\n", key)
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
		fmt.Fprintf(opts.stderr, "mlop traffic: no Deployment %q in the cluster yet%s\n", target, hint)
		return 1
	}
	rt.Cluster.SetTraffic(target, *requests, *errors, *p95Ms)
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop traffic: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop traffic: %v\n", err)
		return 1
	}
	m, _ := rt.Cluster.Metrics(target)
	d := rt.Registry.Get(key).Deployment
	p95 := ""
	if m.LatencyP95Ms > 0 {
		p95 = fmt.Sprintf(", p95 %.0fms", m.LatencyP95Ms)
	}
	fmt.Fprintf(opts.stdout, "%s: traffic %d requests, %d errors (%.2f%% error rate%s) — cumulative %d requests, %d errors (%.2f%%) — phase=%s (%d/%d ready)\n",
		target, *requests, *errors, 100*float64(*errors)/float64(*requests), p95,
		m.Requests, m.Errors, 100*m.ErrorRate, res.Phase, d.Status.ReadyReplicas, desiredReplicas(d))
	return 0
}
