// Package metrics is the operator's observability surface: a
// dependency-free Prometheus-style collector (counters and gauges,
// rendered as text exposition) and a Kubernetes-style event
// recorder (a bounded ring of Normal/Warning events per deployment).
// The reconciler records every reconcile outcome, rollout decision
// and scaling action here; the CLI and the demo print them. Nothing
// in this package talks to a cluster — the real Prometheus scrape
// endpoint and the Kubernetes event sink land with the
// controller-runtime phase.
package metrics
