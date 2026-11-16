package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/completion"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// cmdCompletion implements `mlop completion <bash|zsh|fish>`: print
// the shell completion script for mlop to stdout. It is read-only
// and touches no state. Install it with, e.g.:
//
//	mlop completion bash | sudo tee /etc/bash_completion.d/mlop
//	mlop completion zsh > "${fpath[1]}/_mlop"
//	mlop completion fish > ~/.config/fish/completions/mlop.fish
func cmdCompletion(opts *options, args []string) int {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop completion <bash|zsh|fish>\n")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(opts.stderr, "Usage: mlop completion <bash|zsh|fish>\n")
		return 2
	}
	script, err := completion.Script(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop completion: %v\n", err)
		return 1
	}
	fmt.Fprint(opts.stdout, script)
	return 0
}

// cmdCompleteNames implements the hidden `mlop __complete-names`:
// print one registered deployment name per line, for the shell
// completion scripts to complete against. It is deliberately not in
// the usage text — it is a completion helper, not a user command.
// Names print bare in the default namespace and as "ns/name"
// elsewhere, matching what the other commands accept. An empty
// state (or no state file yet) prints nothing and exits 0: a shell
// completing names must never error just because nothing is
// deployed yet.
func cmdCompleteNames(opts *options, args []string) int {
	fs := flag.NewFlagSet("__complete-names", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(opts.stderr, "Usage: mlop __complete-names\n")
		return 2
	}
	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop __complete-names: %v\n", err)
		return 1
	}
	rt := st.Restore()
	for _, e := range rt.Registry.List() {
		d := e.Deployment
		if namespaceOf(d) == "default" {
			fmt.Fprintln(opts.stdout, d.Name)
		} else {
			fmt.Fprintf(opts.stdout, "%s/%s\n", namespaceOf(d), d.Name)
		}
	}
	return 0
}
