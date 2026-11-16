package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdDrift implements `mlop drift <name>` and `mlop drift --all
// [--json]`: compare the live cluster against the desired state
// and report, per managed object, whether it is in sync, drifted,
// missing, or present-but-not-desired, with one reason per moved
// field. It never changes state — drift is a read, the fix is a
// re-apply (or a manifest update, when the live change was the
// intended one and the spec is what is stale).
//
// Exit code is the point of the command in scripts and CI: 0 when
// everything is in sync, 1 when any drift is found, 2 for usage
// errors, unknown deployments, and state-file errors.
func cmdDrift(opts *options, args []string) int {
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop drift <name> | mlop drift --all [--json]\n")
	}
	all := fs.Bool("all", false, "report drift for every registered deployment")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if (*all && fs.NArg() > 0) || (!*all && fs.NArg() == 0) {
		fmt.Fprintln(opts.stderr, "mlop drift: give a deployment name or --all (exactly one)")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop drift: %v\n", err)
		return 1
	}
	rt := st.Restore()

	var reports []controller.DeploymentDrift
	if *all {
		reports, err = rt.Reconciler.DriftAll()
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop drift: %v\n", err)
			return 1
		}
	} else {
		key := resolveKey(fs.Arg(0))
		rep, err := rt.Reconciler.Drift(key)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop drift: %v\n", err)
			return 1
		}
		reports = []controller.DeploymentDrift{rep}
	}

	if *jsonOut {
		var v any = reports
		if !*all {
			v = reports[0]
		}
		enc := json.NewEncoder(opts.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(opts.stderr, "mlop drift: %v\n", err)
			return 1
		}
	} else {
		for _, rep := range reports {
			printDriftReport(opts, rep)
		}
	}

	for _, rep := range reports {
		if !rep.InSync {
			return 1
		}
	}
	return 0
}

// printDriftReport prints one deployment's drift in text form: a
// one-line verdict when in sync, and one line per offending object
// with its reasons when not. In-sync objects are not listed —
// drift is a report about what moved, not an inventory.
func printDriftReport(opts *options, rep controller.DeploymentDrift) {
	if rep.InSync {
		fmt.Fprintf(opts.stdout, "in sync: %s\n", rep.Deployment)
		return
	}
	fmt.Fprintf(opts.stdout, "drift: %s\n", rep.Deployment)
	for _, res := range rep.Resources {
		if res.Status == controller.DriftInSync {
			continue
		}
		fmt.Fprintf(opts.stdout, "  %s %s: %s\n", res.Kind, res.Key, res.Status)
		for _, reason := range res.Reasons {
			fmt.Fprintf(opts.stdout, "    - %s\n", reason)
		}
	}
	if rep.Paused {
		fmt.Fprintln(opts.stdout, "  note: paused; drift will not be corrected until resume")
	}
}
