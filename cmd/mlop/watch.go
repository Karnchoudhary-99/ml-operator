package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// watchEvent is one line of `mlop watch --json`: a single object
// per phase transition, so a script can jq the stream instead of
// parsing human text.
type watchEvent struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Phase     api.Phase `json:"phase"`
	Ready     int32     `json:"ready"`
	Desired   int32     `json:"desired"`
	Message   string    `json:"message"`
	Elapsed   string    `json:"elapsed"`
}

// cmdWatch implements `mlop watch <name> [--all]`: poll the
// deployment(s), reconciling on every tick, and print each phase
// transition until every watched deployment reaches a terminal
// state. Exit 0 when all become Available, 1 when any turns Failed
// or Degraded, 2 when the timeout expires first. The fake cluster
// has no pods, so a deployment only becomes ready when `mlop ready`
// is run in another shell — watch is the "wait for the rollout"
// half of that pair.
func cmdWatch(opts *options, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop watch <name> [--interval 1s] [--timeout 5m] [--json] [--all]\n")
	}
	interval := fs.Duration("interval", time.Second, "how often to reconcile and re-check the phase")
	timeout := fs.Duration("timeout", 5*time.Minute, "give up after this long without a terminal phase")
	jsonOut := fs.Bool("json", false, "print one JSON object per phase transition instead of text")
	all := fs.Bool("all", false, "watch every deployment instead of one name")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if *all && fs.NArg() != 0 {
		fmt.Fprintln(opts.stderr, "mlop watch: --all takes no deployment name")
		return 2
	}
	if !*all && fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop watch: exactly one deployment name is required (or --all)")
		return 2
	}
	if *interval <= 0 {
		fmt.Fprintln(opts.stderr, "mlop watch: --interval must be positive")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(opts.stderr, "mlop watch: --timeout must be positive")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop watch: %v\n", err)
		return 1
	}
	rt := st.Restore()

	var keys []string
	if *all {
		for _, e := range rt.Registry.List() {
			keys = append(keys, e.Deployment.Key())
		}
		if len(keys) == 0 {
			fmt.Fprintln(opts.stderr, "mlop watch: no deployments to watch")
			return 1
		}
	} else {
		key := resolveKey(fs.Arg(0))
		if rt.Registry.Get(key) == nil {
			fmt.Fprintf(opts.stderr, "mlop watch: no deployment %q\n", key)
			return 1
		}
		keys = []string{key}
	}

	// Poll on the configured interval until the timeout: long
	// enough for a canary step to advance by default, short
	// enough to fail loudly instead of hanging a script forever.
	start := time.Now()
	deadline := start.Add(*timeout)
	// Print only when a phase changes: a steady Progressing
	// deployment would otherwise spam one line per second for
	// minutes. The first snapshot of each key always prints.
	lastPhase := make(map[string]api.Phase, len(keys))
	seen := make(map[string]bool, len(keys))
	for {
		phases := make(map[string]api.Phase, len(keys))
		for _, key := range keys {
			res, err := rt.Reconciler.Reconcile(key)
			if err != nil {
				fmt.Fprintf(opts.stderr, "mlop watch: reconcile %s: %v\n", key, err)
				return 1
			}
			d := rt.Registry.Get(key).Deployment
			phases[key] = res.Phase
			if !seen[key] || res.Phase != lastPhase[key] {
				if *jsonOut {
					ev := watchEvent{
						Name: d.Name, Namespace: namespaceOf(d),
						Phase: res.Phase, Ready: d.Status.ReadyReplicas,
						Desired: desiredReplicas(d), Message: d.Status.Message,
						Elapsed: time.Since(start).Round(time.Millisecond).String(),
					}
					enc := json.NewEncoder(opts.stdout)
					if err := enc.Encode(ev); err != nil {
						fmt.Fprintf(opts.stderr, "mlop watch: %v\n", err)
						return 1
					}
				} else {
					fmt.Fprintf(opts.stdout, "%s %s: %s %d/%d ready\n",
						time.Now().Format("15:04:05"), key, res.Phase,
						d.Status.ReadyReplicas, desiredReplicas(d))
				}
				seen[key] = true
				lastPhase[key] = res.Phase
			}
		}
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop watch: %v\n", err)
			return 1
		}
		// Any failure ends the watch at once; success needs
		// every watched deployment Available.
		for _, key := range keys {
			if phases[key] == api.PhaseFailed || phases[key] == api.PhaseDegraded {
				return 1
			}
		}
		allAvailable := true
		for _, key := range keys {
			if phases[key] != api.PhaseAvailable {
				allAvailable = false
				break
			}
		}
		if allAvailable {
			return 0
		}
		if time.Now().After(deadline) {
			if *all {
				fmt.Fprintf(opts.stderr, "mlop watch: timed out after %s waiting for all deployments\n", *timeout)
			} else {
				fmt.Fprintf(opts.stderr, "mlop watch: timed out after %s waiting for %s\n", *timeout, keys[0])
			}
			return 2
		}
		time.Sleep(*interval)
	}
}
