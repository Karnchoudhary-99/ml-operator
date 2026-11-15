// Package crd generates the Kubernetes manifests the operator needs
// to run on a real cluster: the CustomResourceDefinition for
// ModelDeployment, and the install bundle (RBAC + operator
// Deployment). The operator itself stays dependency-free, so there
// is no YAML library here either: a small ordered-node emitter in
// yaml.go renders the manifests, and the schema in schema.go is the
// single source of truth for the CRD's openAPIV3 validation — it
// mirrors internal/api.Validate, so a spec the API server admits is
// one the operator accepts, and vice versa.
//
// The generated files are checked in under examples/ (crd.yaml,
// install.yaml) and golden-tested: `mlop crd` and `mlop install`
// must reproduce them byte for byte. Regenerate them with those
// commands after any change to the schema or the install bundle.
package crd
