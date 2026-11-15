package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/Karnchoudhary-99/ml-operator/internal/manifest"
)

// LoadFile reads operator configuration from path — YAML (the
// documented subset) or JSON — and applies it over Defaults. Keys
// absent from the file keep their defaults; unknown keys are an
// error, so a typo fails loudly instead of being silently ignored.
// A missing or unparseable file is an error: the operator must not
// start on defaults it was never told to use.
func LoadFile(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config file %s: %w", path, err)
	}
	tree, err := manifest.ParseTree(data)
	if err != nil {
		return cfg, fmt.Errorf("parse config file %s: %w", path, err)
	}
	known := []string{
		"namespace", "metricsAddr", "reconcileIntervalSeconds",
		"applyMaxAttempts", "applyRetryBackoffMs",
	}
	for k := range tree {
		knownKey := false
		for _, known := range known {
			if k == known {
				knownKey = true
				break
			}
		}
		if !knownKey {
			return cfg, fmt.Errorf("config file %s: unknown key %q", path, k)
		}
	}
	if v, ok := tree["namespace"]; ok {
		s, err := stringField(v, "namespace")
		if err != nil {
			return cfg, err
		}
		cfg.Namespace = s
	}
	if v, ok := tree["metricsAddr"]; ok {
		s, err := stringField(v, "metricsAddr")
		if err != nil {
			return cfg, err
		}
		cfg.MetricsAddr = s
	}
	if v, ok := tree["reconcileIntervalSeconds"]; ok {
		n, err := intField(v, "reconcileIntervalSeconds")
		if err != nil {
			return cfg, err
		}
		cfg.ReconcileIntervalSeconds = int(n)
	}
	if v, ok := tree["applyMaxAttempts"]; ok {
		n, err := intField(v, "applyMaxAttempts")
		if err != nil {
			return cfg, err
		}
		cfg.ApplyMaxAttempts = int(n)
	}
	if v, ok := tree["applyRetryBackoffMs"]; ok {
		n, err := intField(v, "applyRetryBackoffMs")
		if err != nil {
			return cfg, err
		}
		cfg.ApplyRetryBackoffMs = int(n)
	}
	return cfg, nil
}

// stringField reads a string value from the parsed tree.
func stringField(v any, path string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: must be a string", path)
	}
	return s, nil
}

// intField reads an integer value. YAML ints arrive as int64, JSON
// numbers as float64; both are accepted when they are whole numbers.
func intField(v any, path string) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("%s: must be a whole number", path)
		}
		return int64(n), nil
	case string:
		// YAML floats arrive as strings ("2.5"); plain ints too.
		if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
			return parsed, nil
		}
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: must be a number, got %q", path, n)
		}
		if f != float64(int64(f)) {
			return 0, fmt.Errorf("%s: must be a whole number", path)
		}
		return int64(f), nil
	default:
		return 0, fmt.Errorf("%s: must be a number", path)
	}
}
