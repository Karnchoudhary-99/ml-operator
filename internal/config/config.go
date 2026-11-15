package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Config is the operator's runtime configuration. Zero values are
// never valid on their own — call Defaults and then override from a
// file, the environment, or flags.
type Config struct {
	// Namespace is the namespace the operator watches for
	// ModelDeployments.
	Namespace string
	// MetricsAddr is the address the /metrics and /healthz server
	// listens on (":8080", "127.0.0.1:9090", or "" to disable it).
	MetricsAddr string
	// ReconcileIntervalSeconds is how often the operator re-reconciles
	// every registered deployment even when nothing changed, so drift
	// is caught without waiting for an event. 0 disables the resync.
	ReconcileIntervalSeconds int
	// ApplyMaxAttempts is how many times one cluster operation is
	// tried before the reconcile gives up on it.
	ApplyMaxAttempts int
	// ApplyRetryBackoffMs is the base delay between attempts; each
	// retry waits twice the previous one.
	ApplyRetryBackoffMs int
}

// Defaults returns the built-in configuration: the "default"
// namespace, metrics on :8080, a 30-second resync, and the same retry
// budget the reconciler already used (3 attempts, 100ms base).
func Defaults() Config {
	return Config{
		Namespace:                "default",
		MetricsAddr:              ":8080",
		ReconcileIntervalSeconds: 30,
		ApplyMaxAttempts:         3,
		ApplyRetryBackoffMs:      100,
	}
}

// Validate returns every problem with the configuration, or nil when
// it is safe to run with.
func (c Config) Validate() []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	if !isDNS1123Subdomain(c.Namespace) {
		add("namespace %q must be a DNS-1123 subdomain (lowercase letters, digits, '-', '.')", c.Namespace)
	}
	if c.MetricsAddr != "" {
		_, port, err := net.SplitHostPort(c.MetricsAddr)
		if err != nil {
			add("metricsAddr %q must be a host:port address: %v", c.MetricsAddr, err)
		} else if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
			// SplitHostPort accepts a non-numeric port, so check it
			// explicitly: ":abc" is not an address we can listen on.
			add("metricsAddr %q must have a numeric port between 0 and 65535, got %q", c.MetricsAddr, port)
		}
	}
	if c.ReconcileIntervalSeconds < 0 {
		add("reconcileIntervalSeconds must be >= 0, got %d", c.ReconcileIntervalSeconds)
	}
	if c.ApplyMaxAttempts < 1 {
		add("applyMaxAttempts must be >= 1, got %d", c.ApplyMaxAttempts)
	}
	if c.ApplyRetryBackoffMs < 0 {
		add("applyRetryBackoffMs must be >= 0, got %d", c.ApplyRetryBackoffMs)
	}
	return errs
}

// dns1123Re mirrors internal/api's subdomain check (kept local so
// config stays independent of the deployment types).
var dns1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func isDNS1123Subdomain(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !dns1123Re.MatchString(s) {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
	}
	return true
}
