package config

import (
	"fmt"
	"strconv"
)

// Overrides is a sparse set of configuration values: only non-nil
// fields apply, so callers can layer sources (file, environment,
// flags) without knowing which keys each source set. Precedence is
// defaults < file < environment < flags.
type Overrides struct {
	Namespace                *string
	MetricsAddr              *string
	ReconcileIntervalSeconds *int
	ApplyMaxAttempts         *int
	ApplyRetryBackoffMs      *int
}

// Apply returns c with every set field of o applied.
func (c Config) Apply(o Overrides) Config {
	if o.Namespace != nil {
		c.Namespace = *o.Namespace
	}
	if o.MetricsAddr != nil {
		c.MetricsAddr = *o.MetricsAddr
	}
	if o.ReconcileIntervalSeconds != nil {
		c.ReconcileIntervalSeconds = *o.ReconcileIntervalSeconds
	}
	if o.ApplyMaxAttempts != nil {
		c.ApplyMaxAttempts = *o.ApplyMaxAttempts
	}
	if o.ApplyRetryBackoffMs != nil {
		c.ApplyRetryBackoffMs = *o.ApplyRetryBackoffMs
	}
	return c
}

// envPrefix is the prefix for every operator environment variable.
const envPrefix = "ML_OPERATOR_"

// FromEnv reads the ML_OPERATOR_* variables through getenv (os.Getenv
// in production; tests pass a map lookup). An invalid value is an
// error — a mistyped interval must fail startup, not silently fall
// back to a default the operator was never told to use.
func FromEnv(getenv func(string) string) (Overrides, error) {
	var o Overrides
	if v := getenv(envPrefix + "NAMESPACE"); v != "" {
		o.Namespace = &v
	}
	if v := getenv(envPrefix + "METRICS_ADDR"); v != "" {
		o.MetricsAddr = &v
	}
	if v := getenv(envPrefix + "RECONCILE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return o, fmt.Errorf("%sRECONCILE_INTERVAL_SECONDS: must be a number, got %q", envPrefix, v)
		}
		o.ReconcileIntervalSeconds = &n
	}
	if v := getenv(envPrefix + "APPLY_MAX_ATTEMPTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return o, fmt.Errorf("%sAPPLY_MAX_ATTEMPTS: must be a number, got %q", envPrefix, v)
		}
		o.ApplyMaxAttempts = &n
	}
	if v := getenv(envPrefix + "APPLY_RETRY_BACKOFF_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return o, fmt.Errorf("%sAPPLY_RETRY_BACKOFF_MS: must be a number, got %q", envPrefix, v)
		}
		o.ApplyRetryBackoffMs = &n
	}
	return o, nil
}
