// Package config holds the operator's runtime configuration: the
// namespace to watch, the metrics address, the resync interval, and
// the cluster retry budget.
//
// Sources, weakest to strongest: built-in defaults, a config file
// (YAML or JSON, --config), environment variables (ML_OPERATOR_*),
// then CLI flags. A bad value in any source is an error at startup,
// never a silent fallback — an operator running on the wrong
// namespace or retry budget is worse than one that refuses to start.
package config
