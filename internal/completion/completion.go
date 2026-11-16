package completion

import (
	"fmt"
	"strings"
)

// Command describes one mlop command for completion purposes: its
// flags, whether it takes a deployment name, and any subcommands.
// This table is the single source of truth the bash, zsh, and fish
// generators all render from, so the three scripts cannot drift
// apart — or from the commands in cmd/mlop/main.go.
type Command struct {
	// Name is the command word, e.g. "status".
	Name string
	// Description is the one-line help shown by zsh and fish.
	Description string
	// Flags are the flags the command accepts, e.g. "--json" or "-f".
	Flags []string
	// TakesName means the command takes a deployment name as its
	// positional argument (completed from `mlop __complete-names`).
	TakesName bool
	// TakesFile means the positional argument is a file path
	// (backup/restore take a backup file). Manifests for
	// apply/validate/diff arrive through -f/-d flags instead, which
	// the flag specs already complete as files.
	TakesFile bool
	// Subcommands, e.g. rollout -> ["status"].
	Subcommands []string
}

// Commands returns the mlop command table in usage order.
func Commands() []Command {
	return []Command{
		{Name: "apply", Description: "validate + register + reconcile a deployment", Flags: []string{"-f", "-d", "--dry-run"}},
		{Name: "validate", Description: "preflight check a deployment or manifest", Flags: []string{"-f", "--json", "--strict"}, TakesName: true},
		{Name: "diff", Description: "show the apply plan without changing state", Flags: []string{"-f", "--json"}, TakesName: true},
		{Name: "status", Description: "show deployments and their phases", Flags: []string{"--json"}, TakesName: true},
		{Name: "logs", Description: "show recorded events for a deployment", Flags: []string{"--tail", "--json"}, TakesName: true},
		{Name: "history", Description: "show the deployment audit trail", Flags: []string{"--tail", "--json"}, TakesName: true},
		{Name: "rollout", Description: "show canary or blue-green rollout progress", Subcommands: []string{"status"}, TakesName: true},
		{Name: "ready", Description: "simulate pods passing the readiness gate", Flags: []string{"--replicas", "--canary", "--green"}, TakesName: true},
		{Name: "load", Description: "simulate the model-load probe", Flags: []string{"--canary", "--green", "--loading", "--fail"}, TakesName: true},
		{Name: "rollback", Description: "restore the previous image/version", TakesName: true},
		{Name: "pause", Description: "suspend reconciliation for a deployment", TakesName: true},
		{Name: "resume", Description: "clear the pause and reconcile immediately", TakesName: true},
		{Name: "watch", Description: "poll until a deployment reaches a terminal phase", Flags: []string{"--interval", "--timeout", "--json", "--all"}, TakesName: true},
		{Name: "backup", Description: "snapshot all operator state to a file", Flags: []string{"--force"}, TakesFile: true},
		{Name: "restore", Description: "verify a backup and restore state from it", Flags: []string{"--force"}, TakesFile: true},
		{Name: "crd", Description: "print the ModelDeployment CustomResourceDefinition"},
		{Name: "install", Description: "print the operator install bundle", Flags: []string{"--namespace", "--image"}},
		{Name: "completion", Description: "print a shell completion script", Subcommands: []string{"bash", "zsh", "fish"}},
		{Name: "version", Description: "print mlop version"},
		{Name: "help", Description: "show mlop usage"},
	}
}

// Shells returns the shells mlop generates completions for.
func Shells() []string {
	return []string{"bash", "zsh", "fish"}
}

// Script renders the completion script for one shell.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashScript(), nil
	case "zsh":
		return zshScript(), nil
	case "fish":
		return fishScript(), nil
	default:
		return "", fmt.Errorf("unknown shell %q (want one of: %s)", shell, strings.Join(Shells(), ", "))
	}
}

// commandNames returns the command words in usage order.
func commandNames() []string {
	cmds := Commands()
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

// nameCommands returns the commands that take a deployment name.
func nameCommands() []Command {
	var out []Command
	for _, c := range Commands() {
		if c.TakesName {
			out = append(out, c)
		}
	}
	return out
}

// valueFlags are the flags that take a value: completing right after
// one of these should offer that value's kind (a file for -f/-d,
// nothing mlop can know for the rest), never a flag or a name.
func valueFlags() []string {
	return []string{"-f", "-d", "--tail", "--replicas", "--interval", "--timeout", "--namespace", "--image"}
}
