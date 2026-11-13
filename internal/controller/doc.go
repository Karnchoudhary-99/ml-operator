// Package controller holds the reconciler: it diffs the desired
// serving state against the live state, builds an apply plan, and
// executes rollouts (recreate, rolling update, canary) with health
// gates before traffic shifts.
package controller
