package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/config"
)

func TestFlagOverridesOnlySetFlags(t *testing.T) {
	set := map[string]bool{"namespace": true, "reconcile-interval": true}
	o := flagOverrides(set, "ml-models", ":9090", 90*time.Second, 250*time.Millisecond, 5)
	if o.Namespace == nil || *o.Namespace != "ml-models" {
		t.Errorf("namespace override missing, got %+v", o)
	}
	if o.ReconcileIntervalSeconds == nil || *o.ReconcileIntervalSeconds != 90 {
		t.Errorf("interval override missing, got %+v", o)
	}
	if o.MetricsAddr != nil || o.ApplyMaxAttempts != nil || o.ApplyRetryBackoffMs != nil {
		t.Errorf("unset flags must not override, got %+v", o)
	}
}

func TestFlagOverridesRoundsDurations(t *testing.T) {
	set := map[string]bool{"reconcile-interval": true, "apply-retry-backoff": true}
	o := flagOverrides(set, "", "", 1500*time.Millisecond, 2500*time.Millisecond, 0)
	if o.ReconcileIntervalSeconds == nil || *o.ReconcileIntervalSeconds != 2 {
		t.Errorf("1500ms interval should round to 2s, got %+v", o)
	}
	if o.ApplyRetryBackoffMs == nil || *o.ApplyRetryBackoffMs != 2500 {
		t.Errorf("2500ms backoff should be 2500ms, got %+v", o)
	}
}

func TestLoadOperatorConfigValidates(t *testing.T) {
	ns := "Bad_Namespace"
	if _, err := loadOperatorConfig("", config.Overrides{Namespace: &ns}); err == nil {
		t.Fatalf("expected an error for an invalid namespace, got none")
	} else if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q should mention namespace", err)
	}
}

func TestLoadOperatorConfigMissingFile(t *testing.T) {
	if _, err := loadOperatorConfig("/nonexistent/operator.yaml", config.Overrides{}); err == nil {
		t.Fatalf("expected an error for a missing config file, got none")
	}
}
