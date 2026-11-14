// Package history is the operator's deployment audit trail: one
// bounded, append-only log per deployment recording every
// generation/phase/image transition the reconciler makes — first
// deploy, version change, promotion, rollback, abort, failure.
// It answers "what happened to this model, and when?" without
// digging through events or the cluster. Entries are plain data
// (JSON), so the mlop state file persists them verbatim; nothing
// in this package talks to a cluster or a registry.
package history
