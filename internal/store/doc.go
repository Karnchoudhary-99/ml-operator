// Package store persists the mlop CLI's working state to a JSON
// file: the registered deployments (with status), the simulated
// cluster, the in-flight canary rollout states, and the recorded
// events. The operator has no real cluster yet, so the state file is
// the shared memory between separate mlop invocations — apply in
// one process, status in the next, and both see the same world.
//
// Known limitation: the autoscaler's scale-down stabilization
// window is in-memory only, so it restarts on every invocation.
// The persisted state is still correct — the worst case is a
// scale-down applying one invocation sooner than the 5-minute
// window would allow.
package store
