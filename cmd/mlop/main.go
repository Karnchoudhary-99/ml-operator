// Command mlop is the operator's command-line client. It talks to
// the same in-memory world the operator demo uses — registry,
// reconciler, fake cluster — persisted to a JSON state file between
// invocations (see internal/store), until the controller-runtime
// phase connects it to a real cluster.
//
// Usage:
//
//	mlop [--state path] <command> [args]
//
// Commands: apply, status, logs, rollout, ready, load, rollback, version.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

var version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options carries the shared flags and output writers for one mlop
// invocation. Tests call run with buffers instead of os.Stdout.
type options struct {
	statePath string
	stdout    io.Writer
	stderr    io.Writer
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mlop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
	}
	statePath := fs.String("state", store.DefaultPath(), "path to the mlop state file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := &options{statePath: *statePath, stdout: stdout, stderr: stderr}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch rest[0] {
	case "apply":
		return cmdApply(opts, rest[1:])
	case "status":
		return cmdStatus(opts, rest[1:])
	case "logs":
		return cmdLogs(opts, rest[1:])
	case "rollout":
		return cmdRollout(opts, rest[1:])
	case "ready":
		return cmdReady(opts, rest[1:])
	case "load":
		return cmdLoad(opts, rest[1:])
	case "rollback":
		return cmdRollback(opts, rest[1:])
	case "version":
		fmt.Fprintf(stdout, "mlop %s\n", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "mlop: unknown command %q\n\n%s", rest[0], usage)
		return 2
	}
}

const usage = `mlop - the ML Model Operator CLI

Usage:
  mlop [--state path] <command> [args]

Commands:
  apply -f manifest.yaml [--dry-run]   validate + register + reconcile a deployment
  status [name]                        show deployments and their phases
  logs <name> [--tail N]               show recorded events for a deployment
  rollout status <name>                show canary rollout progress
  ready <name> --replicas N [--canary] simulate pods passing the readiness gate
  load <name> [--canary] [--fail]       simulate the model-load probe (loaded, or failed with --fail)
  rollback <name>                      restore the previous image/version
  version                              print mlop version

State:
  Deployments, the simulated cluster, rollout progress and events
  persist in a JSON state file (default ~/.mlop/state.json, or
  --state / $MLOP_STATE), so separate invocations share one world.
`

// reorderFlags moves flag tokens (and the values of flags that take
// one) before positional arguments, so `mlop ready iris-mlp
// --replicas 2` parses the same as `mlop ready --replicas 2
// iris-mlp`. Go's flag package stops at the first positional, so
// without this, flags after the name would be swallowed as extra
// positional arguments. Unknown flags are left for flag.Parse to
// reject with its usual error.
func reorderFlags(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			name := strings.TrimLeft(a, "-")
			if idx := strings.Index(name, "="); idx >= 0 {
				name = name[:idx]
			}
			flags = append(flags, a)
			if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}
