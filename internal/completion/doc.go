// Package completion generates shell completion scripts for mlop.
//
// The scripts are generated from the command table in completion.go,
// which is the single source of truth for what mlop completes: command
// names, per-command flags, and which commands take a deployment name.
// Deployment names are completed dynamically at shell time through the
// hidden `mlop __complete-names` command, so the scripts never go stale
// as deployments come and go.
package completion
