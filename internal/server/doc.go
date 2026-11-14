// Package server exposes the operator's observability endpoints over
// HTTP: the Prometheus text exposition from the metrics collector on
// /metrics, and a liveness probe on /healthz. It is dependency-free
// (net/http only) and safe to run alongside the reconcile loop —
// the collector is already safe for concurrent use.
package server
