// Package manifest parses ModelDeployment manifests for the mlop CLI.
// It accepts the YAML shape shown in the README (a small indentation
// based subset: nested maps, string/int/bool scalars, comments) and
// JSON with the same keys. The operator stays dependency-free, so
// there is no YAML library: anything outside the documented subset
// is rejected with a line number instead of silently misread.
package manifest
