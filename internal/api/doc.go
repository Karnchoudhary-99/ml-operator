// Package api defines the ModelDeployment custom resource types:
// the desired spec, the observed status, validation and defaults.
// It is intentionally dependency-free so the types can be used by
// the operator, the CLI and tests without a Kubernetes client.
package api
