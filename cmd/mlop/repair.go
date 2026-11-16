package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdRepair implements `mlop repair <name> | mlop repair --all
// [--dry-run] [--json]`: the write half of `mlop drift`. It
// re-applies the desired state to every drifted or missing object
// (the same plan the reconciler uses), verifies the fix with a
// fresh drift check, and saves state — cluster objects and the
// DriftRepaired event are persisted, so the repair survives the
// next invocation.
//
// Exit code is the point of the command in scripts and CI: 0 when
// everything is repaired (or already in sync), 1 when something
// was left unrepaired or a repair failed, 2 for usage errors,
// unknown deployments, and state-file errors. A dry run never
// writes and exits 1 when it would have repaired something, so it
// works as a CI gate the same way `mlop drift` does.
func cmdRepair(opts *options, args []string) int {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop repair <name> | mlop repair --all [--dry-run] [--json]\n")
	}
	all := fs.Bool("all", false, "repair drift for every registered deployment")
	dryRun := fs.Bool("dry-run", false, "report what would be repaired without changing anything")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if (*all && fs.NArg() > 0) || (!*all && fs.NArg() == 0) {
		fmt.Fprintln(opts.stderr, "mlop repair: give a deployment name or --all (exactly one)")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop repair: %v\n", err)
		return 1
	}
	rt := st.Restore()

	var reports []controller.DeploymentRepair
	if *all {
		reports, err = rt.Reconciler.RepairAll(*dryRun)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop repair: %v\n", err)
			return 1
		}
	} else {
		key := resolveKey(fs.Arg(0))
		rep, err := rt.Reconciler.Repair(key, *dryRun)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop repair: %v\n", err)
			return 1
		}
		reports = []controller.DeploymentRepair{rep}
	}

	// Repair mutates the cluster and records events; a dry run
	// writes nothing, so it skips the save and the state file
	// stays byte-identical.
	if !*dryRun {
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop repair: %v\n", err)
			return 1
		}
	}

	if *jsonOut {
		var v any = reports
		if !*all {
			v = reports[0]
		}
		enc := json.NewEncoder(opts.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(opts.stderr, "mlop repair: %v\n", err)
			return 1
		}
	} else {
		for _, rep := range reports {
			printRepairReport(opts, rep)
		}
	}

	for _, rep := range reports {
		if !rep.AllFixed() {
			return 1
		}
	}
	return 0
}

// printRepairReport prints one deployment's repair in text form: a
// one-line verdict when nothing was wrong, and one line per object
// that was fixed, left alone, or failed. Unchanged objects are not
// listed — a repair report is about what moved, not an inventory.
func printRepairReport(opts *options, rep controller.DeploymentRepair) {
	if rep.Fixed() == 0 && rep.Left() == 0 && rep.Failed() == 0 {
		fmt.Fprintf(opts.stdout, "in sync: %s\n", rep.Deployment)
		return
	}
	verb := "repaired"
	if rep.DryRun {
		verb = "would repair"
	}
	fmt.Fprintf(opts.stdout, "%s: %s\n", verb, rep.Deployment)
	for _, res := range rep.Results {
		switch res.Outcome {
		case controller.RepairFixed, controller.RepairWouldFix:
			fmt.Fprintf(opts.stdout, "  repaired %s %s: %s\n", res.Kind, res.Key, res.Detail)
		case controller.RepairLeft:
			fmt.Fprintf(opts.stdout, "  left %s %s (%s): %s\n", res.Kind, res.Key, res.Was, res.Detail)
		case controller.RepairFailed:
			fmt.Fprintf(opts.stdout, "  FAILED %s %s: %s\n", res.Kind, res.Key, res.Detail)
		}
	}
	if rep.Paused {
		fmt.Fprintln(opts.stdout, "  note: paused; resume with `mlop resume` before repairing")
	}
}
