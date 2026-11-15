package config

import (
	"testing"
)

func TestApplyOverridesOnlySetFields(t *testing.T) {
	ns, interval := "ml-models", 0
	got := Defaults().Apply(Overrides{Namespace: &ns, ReconcileIntervalSeconds: &interval})
	if got.Namespace != "ml-models" {
		t.Errorf("namespace = %q, want %q", got.Namespace, "ml-models")
	}
	if got.ReconcileIntervalSeconds != 0 {
		t.Errorf("reconcileIntervalSeconds = %d, want 0", got.ReconcileIntervalSeconds)
	}
	def := Defaults()
	if got.MetricsAddr != def.MetricsAddr || got.ApplyMaxAttempts != def.ApplyMaxAttempts {
		t.Errorf("unset fields should keep defaults, got %+v", got)
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{
		"ML_OPERATOR_NAMESPACE":                  "staging",
		"ML_OPERATOR_METRICS_ADDR":               "127.0.0.1:9090",
		"ML_OPERATOR_RECONCILE_INTERVAL_SECONDS": "45",
		"ML_OPERATOR_APPLY_MAX_ATTEMPTS":         "5",
		"ML_OPERATOR_APPLY_RETRY_BACKOFF_MS":     "250",
	}
	o, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	cfg := Defaults().Apply(o)
	if cfg.Namespace != "staging" || cfg.MetricsAddr != "127.0.0.1:9090" ||
		cfg.ReconcileIntervalSeconds != 45 || cfg.ApplyMaxAttempts != 5 ||
		cfg.ApplyRetryBackoffMs != 250 {
		t.Errorf("env overrides not applied, got %+v", cfg)
	}
}

func TestFromEnvEmptyIsNoOverride(t *testing.T) {
	o, err := FromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if o != (Overrides{}) {
		t.Errorf("empty env should produce no overrides, got %+v", o)
	}
}

func TestFromEnvInvalidNumber(t *testing.T) {
	_, err := FromEnv(func(k string) string {
		if k == "ML_OPERATOR_APPLY_MAX_ATTEMPTS" {
			return "many"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("expected an error for a non-numeric env value, got none")
	}
}

func TestPrecedenceFileThenEnvThenFlags(t *testing.T) {
	path := writeConfig(t, "namespace: file-ns\napplyMaxAttempts: 2\n")
	fileCfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	env, err := FromEnv(func(k string) string {
		if k == "ML_OPERATOR_NAMESPACE" {
			return "env-ns"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	attempts := 7
	flags := Overrides{ApplyMaxAttempts: &attempts}
	got := fileCfg.Apply(env).Apply(flags)
	if got.Namespace != "env-ns" {
		t.Errorf("env should beat file for namespace, got %q", got.Namespace)
	}
	if got.ApplyMaxAttempts != 7 {
		t.Errorf("flags should beat file for attempts, got %d", got.ApplyMaxAttempts)
	}
}
