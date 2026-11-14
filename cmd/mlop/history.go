package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/history"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// historyEntryView is the JSON form of one audit-trail entry. It
// mirrors history.Entry field-for-field so scripts get the same
// data the text output shows.
type historyEntryView struct {
	Generation int64     `json:"generation"`
	Image      string    `json:"image"`
	Version    string    `json:"version"`
	Phase      string    `json:"phase"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

// printHistoryJSON prints entries as indented JSON, oldest first,
// like the text output. An empty log prints [] (never null), so
// scripts can always unmarshal an array.
func printHistoryJSON(opts *options, entries []history.Entry) int {
	rows := make([]historyEntryView, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, historyEntryView{
			Generation: e.Generation, Image: e.Image, Version: e.Version,
			Phase: string(e.Phase), Reason: e.Reason, At: e.At,
		})
	}
	enc := json.NewEncoder(opts.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(opts.stderr, "mlop history: %v\n", err)
		return 1
	}
	return 0
}

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
		fmt.Fprint(opts.stderr, "Usage: mlop history <name> [--tail N] [--json]\n")
	}
	tail := fs.Int("tail", 20, "show at most N entries (0 = all)")
	jsonOut := fs.Bool("json", false, "print entries as JSON")
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
		if *jsonOut {
			fmt.Fprintln(opts.stdout, "[]")
			return 0
		}
		fmt.Fprintf(opts.stdout, "no history for %s\n", key)
		return 0
	}
	if *tail > 0 && len(entries) > *tail {
		entries = entries[len(entries)-*tail:]
	}
	if *jsonOut {
		return printHistoryJSON(opts, entries)
	}
	for _, e := range entries {
		fmt.Fprintf(opts.stdout, "%s gen %d %s:%s %-11s %s\n",
			e.At.Format("2006-01-02 15:04:05"), e.Generation,
			e.Image, e.Version, e.Phase, e.Reason)
	}
	return 0
}
