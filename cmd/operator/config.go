package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/config"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
)

// loadOperatorConfig resolves the effective operator configuration:
// defaults, then the --config file, then ML_OPERATOR_* environment
// variables, then the flags explicitly set on the command line. The
// result is validated; a bad value in any source fails startup
// instead of running on a configuration nobody asked for.
func loadOperatorConfig(configPath string, flagOv config.Overrides) (config.Config, error) {
	cfg := config.Defaults()
	if configPath != "" {
		var err error
		cfg, err = config.LoadFile(configPath)
		if err != nil {
			return cfg, err
		}
	}
	env, err := config.FromEnv(os.Getenv)
	if err != nil {
		return cfg, err
	}
	cfg = cfg.Apply(env).Apply(flagOv)
	if errs := cfg.Validate(); len(errs) > 0 {
		return cfg, fmt.Errorf("invalid operator configuration: %v", errors.Join(errs...))
	}
	return cfg, nil
}

// flagOverrides builds Overrides from the flags the user explicitly
// set. set is the set of flag names flag.Visit reported; the values
// are the parsed flag variables. A flag left at its default does not
// override the file or the environment — the flag default only shows
// in --help, it never wins over a configured value.
func flagOverrides(set map[string]bool, namespace, metricsAddr string, reconcileInterval, applyRetryBackoff time.Duration, applyMaxAttempts int) config.Overrides {
	var o config.Overrides
	if set["namespace"] {
		o.Namespace = &namespace
	}
	if set["metrics-addr"] {
		o.MetricsAddr = &metricsAddr
	}
	if set["reconcile-interval"] {
		s := int(math.Round(reconcileInterval.Seconds()))
		o.ReconcileIntervalSeconds = &s
	}
	if set["apply-max-attempts"] {
		o.ApplyMaxAttempts = &applyMaxAttempts
	}
	if set["apply-retry-backoff"] {
		ms := int(applyRetryBackoff.Milliseconds())
		o.ApplyRetryBackoffMs = &ms
	}
	return o
}

// setFlags returns the names of the flags explicitly set on fs.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// applyConfigToReconciler hands the retry budget from the resolved
// configuration to a reconciler. The reconciler's own defaults match
// the config defaults, so this is a no-op for a default config.
func applyConfigToReconciler(cfg config.Config, rec *controller.Reconciler) {
	rec.ApplyMaxAttempts = cfg.ApplyMaxAttempts
	rec.ApplyRetryBackoff = time.Duration(cfg.ApplyRetryBackoffMs) * time.Millisecond
}
