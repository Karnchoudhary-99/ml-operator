package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/crd"
)

// cmdCRD implements `mlop crd`: print the CustomResourceDefinition
// for ModelDeployment. It is read-only and touches no state — the
// CRD is generated from the same schema the operator validates
// against, so the printed document can never drift from the code.
// Pipe it straight into a cluster: `mlop crd | kubectl apply -f -`.
func cmdCRD(opts *options, args []string) int {
	fs := flag.NewFlagSet("crd", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop crd\n")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(opts.stderr, "Usage: mlop crd\n")
		return 2
	}
	out, err := crd.CRD()
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop crd: %v\n", err)
		return 1
	}
	fmt.Fprint(opts.stdout, out)
	return 0
}

// cmdInstall implements `mlop install [--namespace N] [--image I]`:
// print the full install bundle (Namespace, ServiceAccount,
// ClusterRole, ClusterRoleBinding, operator Deployment) for a real
// cluster. It is read-only and touches no state. Pipe it straight
// into a cluster: `mlop install | kubectl apply -f -`.
func cmdInstall(opts *options, args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop install [--namespace name] [--image image]\n")
	}
	namespace := fs.String("namespace", "", "operator namespace (default "+crd.DefaultNamespace+")")
	image := fs.String("image", "", "operator container image (default "+crd.DefaultOperatorImage+")")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(opts.stderr, "Usage: mlop install [--namespace name] [--image image]\n")
		return 2
	}
	out, err := crd.InstallBundle(*namespace, *image)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop install: %v\n", err)
		return 1
	}
	fmt.Fprint(opts.stdout, out)
	return 0
}
