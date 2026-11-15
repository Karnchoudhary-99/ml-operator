package main

import (
	"flag"
	"fmt"

	"github.com/Karnchoudhary-99/ml-operator/internal/backup"
)

// cmdBackup implements `mlop backup <file> [--force]`: snapshot
// the whole operator state (deployments, cluster, rollouts,
// events, history) into a checksummed backup file. The backup is
// the last copy of a world, so overwriting an existing file needs
// --force.
func cmdBackup(opts *options, args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop backup <file> [--force]\n")
	}
	force := fs.Bool("force", false, "overwrite an existing backup file")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop backup: exactly one backup file path is required")
		return 2
	}
	summary, err := backup.Write(opts.statePath, fs.Arg(0), version, *force)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(opts.stdout, "backed up %d deployment(s) to %s\n", summary.Deployments, summary.Path)
	fmt.Fprintf(opts.stdout, "  created: %s  checksum: %s  size: %d bytes\n",
		summary.CreatedAt.Format("2006-01-02 15:04:05 UTC"), summary.ShortChecksum(), summary.Bytes)
	return 0
}

// cmdRestore implements `mlop restore <file> [--force]`: verify a
// backup's checksum and write its state back into the state file.
// It refuses to overwrite a live state that already holds
// deployments unless --force is passed — a restore should never
// silently drop a running world.
func cmdRestore(opts *options, args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop restore <file> [--force]\n")
	}
	force := fs.Bool("force", false, "overwrite a state file that already holds deployments")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(opts.stderr, "mlop restore: exactly one backup file path is required")
		return 2
	}
	summary, err := backup.Restore(fs.Arg(0), opts.statePath, *force)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop restore: %v\n", err)
		return 1
	}
	fmt.Fprintf(opts.stdout, "restored %d deployment(s) from %s (backup from %s, checksum %s)\n",
		summary.Deployments, summary.Path,
		summary.CreatedAt.Format("2006-01-02 15:04:05 UTC"), summary.ShortChecksum())
	return 0
}
