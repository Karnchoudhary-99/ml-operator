package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdLogs implements `mlop logs <name> [--tail N]`: the recorded
// operator events for one deployment, newest first. Events are read
// from the state file as recorded — logs never reconciles, so
// reading the log cannot change what it reports.
func cmdLogs(opts *options, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop logs <name> [--tail N]\n")
	}
	tail := fs.Int("tail", 20, "show at most N events (0 = all)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop logs: exactly one deployment name is required")
		return 2
	}
	key := resolveKey(fs.Arg(0))

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop logs: %v\n", err)
		return 1
	}
	rt := st.Restore()
	if rt.Registry.Get(key) == nil {
		fmt.Fprintf(opts.stderr, "mlop logs: no deployment %q\n", key)
		return 1
	}
	events := rt.Reconciler.Events.EventsFor(key)
	if len(events) == 0 {
		fmt.Fprintf(opts.stdout, "no events for %s\n", key)
		return 0
	}
	if *tail > 0 && len(events) > *tail {
		events = events[:*tail]
	}
	for _, e := range events {
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" (x%d)", e.Count)
		}
		fmt.Fprintf(opts.stdout, "%s %-7s %-16s %s%s\n",
			e.LastSeen.Format("15:04:05"), e.Type, e.Reason, e.Message, count)
	}
	return 0
}
