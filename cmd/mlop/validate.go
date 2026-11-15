package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// validateView is the JSON form of a validation report: the
// deployment, whether it passed, and the errors and warnings found.
// Errors and warnings are always arrays (never null) so scripts can
// iterate them without a nil check.
type validateView struct {
	Deployment string   `json:"deployment"`
	Valid      bool     `json:"valid"`
	Strict     bool     `json:"strict"`
	Errors     []string `json:"errors"`
	Warnings   []string `json:"warnings"`
}

// cmdValidate implements `mlop validate -f manifest.yaml` and
// `mlop validate <name> [--json] [--strict]`: parse (or load) a
// deployment, run Validate on the defaulted spec and Lint on the
// spec as written, and report errors and warnings without touching
// any state. A manifest is checked as written; a stored deployment
// is checked as it was applied (defaults already filled in), so a
// stored check cannot see choices the defaults already overwrote.
//
// Exit codes: 0 when valid (warnings allowed), 1 when invalid,
// 2 for usage errors. --strict turns warnings into failures, for
// CI gates that want a clean bill of health before an apply.
func cmdValidate(opts *options, args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop validate <name> | mlop validate -f manifest.yaml [--json] [--strict]\n")
	}
	file := fs.String("f", "", "manifest file (YAML or JSON)")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	strict := fs.Bool("strict", false, "treat warnings as errors")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if (*file == "") == (fs.NArg() == 0) {
		fmt.Fprintln(opts.stderr, "mlop validate: give a deployment name or -f manifest.yaml (exactly one)")
		return 2
	}

	var d api.ModelDeployment
	if *file != "" {
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop validate: read %s: %v\n", *file, err)
			return 1
		}
		d, err = manifest.Parse(raw)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop validate: %v\n", err)
			return 1
		}
	} else {
		st, err := store.Load(opts.statePath)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop validate: %v\n", err)
			return 1
		}
		rt := st.Restore()
		key := resolveKey(fs.Arg(0))
		entry := rt.Registry.Get(key)
		if entry == nil {
			fmt.Fprintf(opts.stderr, "mlop validate: no deployment %q\n", key)
			return 1
		}
		d = entry.Deployment
	}

	// Lint first, on the spec as written: defaults would hide
	// explicit choices (an explicit minReplicas 0 becomes 1).
	warnings := api.Lint(&d)
	api.ApplyDefaults(&d)
	errs := api.Validate(&d)

	view := validateView{
		Deployment: d.Key(), Strict: *strict,
		Errors:   errStrings(errs),
		Warnings: warnings,
	}
	if view.Errors == nil {
		view.Errors = []string{}
	}
	if view.Warnings == nil {
		view.Warnings = []string{}
	}
	view.Valid = len(view.Errors) == 0 && (!*strict || len(view.Warnings) == 0)

	if *jsonOut {
		enc := json.NewEncoder(opts.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(view); err != nil {
			fmt.Fprintf(opts.stderr, "mlop validate: %v\n", err)
			return 1
		}
	} else if len(view.Errors) > 0 {
		fmt.Fprintf(opts.stderr, "mlop validate: %s is invalid:\n", view.Deployment)
		for _, e := range view.Errors {
			fmt.Fprintf(opts.stderr, "  - %s\n", e)
		}
		for _, w := range view.Warnings {
			fmt.Fprintf(opts.stderr, "  warning: %s\n", w)
		}
	} else {
		fmt.Fprintf(opts.stdout, "valid: %s\n", view.Deployment)
		for _, w := range view.Warnings {
			fmt.Fprintf(opts.stdout, "  warning: %s\n", w)
		}
		if len(view.Warnings) == 0 {
			fmt.Fprintln(opts.stdout, "  no warnings")
		}
	}
	if !view.Valid {
		return 1
	}
	return 0
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}
