package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdHistory implements `mlop history <name> [--tail N]`: the
// recorded audit trail for one deployment — every generation /
// phase / image transition the reconciler made, oldest first, so
// it reads like a timeline of what happened to the model. History
// is read from the state file as recorded — history never
// reconciles, so reading it cannot change what it reports.
func cmdHistory(opts *options, args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop history <name> [--tail N]\n")
	}
	tail := fs.Int("tail", 20, "show at most N entries (0 = all)")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop history: exactly one deployment name is required")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop history: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop history: no deployment %q\n", key)
		return 1
	}
	entries := rt.Reconciler.History(key)
	if len(entries) == 0 {
		fmt.Fprintf(opts.stdout, "no history for %s\n", key)
		return 0
	}
	if *tail > 0 && len(entries) > *tail {
		entries = entries[len(entries)-*tail:]
	}
	for _, e := range entries {
		fmt.Fprintf(opts.stdout, "%s gen %d %s:%s %-11s %s\n",
			e.At.Format("2006-01-02 15:04:05"), e.Generation,
			e.Image, e.Version, e.Phase, e.Reason)
	}
	return 0
}
