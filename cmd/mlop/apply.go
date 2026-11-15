package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdApply implements `mlop apply -f manifest.yaml` (or -d a
// directory of manifests): parse the manifest, apply defaults,
// validate, register it (bumping the generation when the spec
// changed), reconcile once against the simulated cluster, and
// persist the new state. --dry-run stops after validation and
// prints what would be applied.
func cmdApply(opts *options, args []string) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop apply -f manifest.yaml [--dry-run]\n       mlop apply -d dir [--dry-run]\n")
	}
	file := fs.String("f", "", "manifest file (YAML or JSON)")
	dir := fs.String("d", "", "directory of manifest files (YAML or JSON, applied in filename order)")
	dryRun := fs.Bool("dry-run", false, "validate only, do not change state")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if *file != "" && *dir != "" {
		fmt.Fprintln(opts.stderr, "mlop apply: -f and -d are mutually exclusive")
		return 2
	}
	if *dir != "" {
		return cmdApplyDir(opts, *dir, *dryRun)
	}
	if *file == "" {
		fmt.Fprintln(opts.stderr, "mlop apply: -f manifest.yaml is required (or -d dir)")
		return 2
	}
	d, warnings, err := loadManifest(*file)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(opts.stdout, "valid: %s (image %s, %s, %d replica(s))\n",
			d.Key(), d.Spec.Model.Image, d.Spec.Strategy, *d.Spec.Replicas)
		for _, w := range warnings {
			fmt.Fprintf(opts.stdout, "  warning: %s\n", w)
		}
		return 0
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	rt := st.Restore()

	line, res, err := applyDeployment(rt, d)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: reconcile %s: %v\n", d.Key(), err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	fmt.Fprintln(opts.stdout, line)
	for _, w := range warnings {
		fmt.Fprintf(opts.stdout, "  warning: %s\n", w)
	}
	for _, a := range res.Actions {
		fmt.Fprintf(opts.stdout, "  - %s %s (%s)\n", a.Kind, a.Key, a.Reason)
	}
	if res.Message != "" {
		fmt.Fprintf(opts.stdout, "  %s\n", res.Message)
	}
	return 0
}

// loadManifest reads, parses, lints and validates one manifest
// file, applying defaults. It returns the deployment and its lint
// warnings; the error carries the read, parse or validation failure
// (validation failures list every bad field, like the old inline
// code did). Warnings never block an apply — they are printed,
// not fatal.
func loadManifest(path string) (api.ModelDeployment, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return api.ModelDeployment{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	d, err := manifest.Parse(raw)
	if err != nil {
		return api.ModelDeployment{}, nil, err
	}
	// Lint the spec as written, before defaults hide explicit
	// choices (an explicit minReplicas 0 becomes 1).
	warnings := api.Lint(&d)
	api.ApplyDefaults(&d)
	if errs := api.Validate(&d); len(errs) > 0 {
		var b strings.Builder
		b.WriteString("manifest is invalid:")
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  - %s", e)
		}
		return api.ModelDeployment{}, warnings, fmt.Errorf("%s", b.String())
	}
	return d, warnings, nil
}

// cmdApplyDir implements `mlop apply -d dir`: apply every
// .yaml/.yml/.json manifest in the directory in filename order,
// loading state once and saving once at the end. Other files are
// ignored (a README may live next to the manifests). A deployment
// key defined by two files in the same directory is rejected as a
// duplicate — re-applying a key that already exists in state is
// fine, that is a normal generation bump. --dry-run validates
// every file and prints what would be applied, without touching
// state.
func cmdApplyDir(opts *options, dir string, dryRun bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: read directory %s: %v\n", dir, err)
		return 1
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	if len(files) == 0 {
		fmt.Fprintf(opts.stderr, "mlop apply: no manifest files (.yaml/.yml/.json) in %s\n", dir)
		return 1
	}

	if dryRun {
		// Validate every file and report all failures at once,
		// so one bad manifest does not hide the next one.
		seen := make(map[string]string)
		valid, failed := 0, 0
		for _, f := range files {
			d, warnings, err := loadManifest(f)
			if err != nil {
				fmt.Fprintf(opts.stderr, "mlop apply: %s: %v\n", f, err)
				failed++
				continue
			}
			if first, dup := seen[d.Key()]; dup {
				fmt.Fprintf(opts.stderr, "mlop apply: %s: duplicate deployment %s (already defined by %s)\n", f, d.Key(), first)
				failed++
				continue
			}
			seen[d.Key()] = f
			fmt.Fprintf(opts.stdout, "valid: %s (image %s, %s, %d replica(s))\n",
				d.Key(), d.Spec.Model.Image, d.Spec.Strategy, *d.Spec.Replicas)
			for _, w := range warnings {
				fmt.Fprintf(opts.stdout, "  warning: %s\n", w)
			}
			valid++
		}
		fmt.Fprintf(opts.stdout, "summary: %d valid, %d failed\n", valid, failed)
		if failed > 0 {
			return 1
		}
		return 0
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
		return 1
	}
	rt := st.Restore()
	// One bad file must not abort the whole batch: report it,
	// keep applying the rest, and save whatever landed. The
	// summary at the end says how many applied and how many
	// failed, and the exit code is 1 when anything failed.
	seen := make(map[string]string)
	applied, unchanged, failed := 0, 0, 0
	for _, f := range files {
		d, warnings, err := loadManifest(f)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop apply: %s: %v\n", f, err)
			failed++
			continue
		}
		if first, dup := seen[d.Key()]; dup {
			fmt.Fprintf(opts.stderr, "mlop apply: %s: duplicate deployment %s (already defined by %s)\n", f, d.Key(), first)
			failed++
			continue
		}
		seen[d.Key()] = f
		line, _, err := applyDeployment(rt, d)
		if err != nil {
			fmt.Fprintf(opts.stderr, "mlop apply: reconcile %s: %v\n", d.Key(), err)
			failed++
			continue
		}
		fmt.Fprintln(opts.stdout, line)
		for _, w := range warnings {
			fmt.Fprintf(opts.stdout, "  warning: %s\n", w)
		}
		// The summary counts honestly: an identical re-apply is
		// "unchanged", not "applied".
		if strings.HasPrefix(line, "unchanged ") {
			unchanged++
		} else {
			applied++
		}
	}
	if applied+unchanged > 0 {
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop apply: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(opts.stdout, "summary: %d applied, %d unchanged, %d failed\n", applied, unchanged, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// applyDeployment registers one parsed manifest in the runtime and
// reconciles it once: a new key starts at generation 1, a changed
// spec bumps the existing generation, an identical re-apply keeps
// it (the registry treats it as a no-op, so nothing is re-rolled).
// It returns the one-line summary cmdApply prints and the
// reconcile result. Callers persist the state themselves, so a
// batch apply can register many deployments before saving once.
func applyDeployment(rt *store.Runtime, d api.ModelDeployment) (string, controller.Result, error) {
	key := d.Key()
	changed := true
	if existing := rt.Registry.Get(key); existing != nil {
		d.Generation = existing.Deployment.Generation
		if registry.SpecsEqual(existing.Deployment.Spec, d.Spec) {
			changed = false
		} else {
			d.Generation++
		}
	} else {
		d.Generation = 1
	}
	rt.Registry.Register(d)

	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		return "", res, err
	}
	verb := "applied"
	if !changed {
		verb = "unchanged"
	}
	return fmt.Sprintf("%s %s (generation %d): phase=%s", verb, key, d.Generation, res.Phase), res, nil
}
