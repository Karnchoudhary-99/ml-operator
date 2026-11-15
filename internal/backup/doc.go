// Package backup snapshots the mlop state file into a portable,
// checksummed backup file — and restores it. The operator keeps
// everything (deployments, the simulated cluster, rollout progress,
// events, history) in one JSON state file (see internal/store), so
// a backup is just that file wrapped in an envelope that records
// when it was taken, how many deployments it holds, and a SHA-256
// of the payload. Restore verifies the checksum before it touches
// the live state, and refuses to clobber a non-empty state file
// unless --force is passed, so a mistyped restore cannot wipe a
// running world.
package backup
