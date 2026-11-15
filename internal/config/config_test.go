package config

import (
	"strings"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	c := Defaults()
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("defaults should validate, got %v", errs)
	}
	if c.Namespace != "default" {
		t.Errorf("default namespace = %q, want %q", c.Namespace, "default")
	}
	if c.MetricsAddr != ":8080" {
		t.Errorf("default metricsAddr = %q, want %q", c.MetricsAddr, ":8080")
	}
	if c.ReconcileIntervalSeconds != 30 {
		t.Errorf("default reconcileIntervalSeconds = %d, want 30", c.ReconcileIntervalSeconds)
	}
	if c.ApplyMaxAttempts != 3 {
		t.Errorf("default applyMaxAttempts = %d, want 3", c.ApplyMaxAttempts)
	}
	if c.ApplyRetryBackoffMs != 100 {
		t.Errorf("default applyRetryBackoffMs = %d, want 100", c.ApplyRetryBackoffMs)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty namespace", func(c *Config) { c.Namespace = "" }, "namespace"},
		{"uppercase namespace", func(c *Config) { c.Namespace = "ML-Models" }, "namespace"},
		{"namespace with underscore", func(c *Config) { c.Namespace = "ml_models" }, "namespace"},
		{"metrics without port", func(c *Config) { c.MetricsAddr = "localhost" }, "metricsAddr"},
		{"metrics with bad port", func(c *Config) { c.MetricsAddr = ":abc" }, "metricsAddr"},
		{"negative interval", func(c *Config) { c.ReconcileIntervalSeconds = -1 }, "reconcileIntervalSeconds"},
		{"zero attempts", func(c *Config) { c.ApplyMaxAttempts = 0 }, "applyMaxAttempts"},
		{"negative backoff", func(c *Config) { c.ApplyRetryBackoffMs = -5 }, "applyRetryBackoffMs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.mutate(&c)
			errs := c.Validate()
			if len(errs) == 0 {
				t.Fatalf("expected a validation error, got none")
			}
			joined := ""
			for _, err := range errs {
				joined += err.Error() + "; "
			}
			if !strings.Contains(joined, tc.wantErr) {
				t.Errorf("error %q should mention %q", joined, tc.wantErr)
			}
		})
	}
}

func TestValidateAllowsDisablingMetricsAndResync(t *testing.T) {
	c := Defaults()
	c.MetricsAddr = ""
	c.ReconcileIntervalSeconds = 0
	if errs := c.Validate(); len(errs) != 0 {
		t.Errorf("empty metricsAddr and 0 interval should validate, got %v", errs)
	}
}
