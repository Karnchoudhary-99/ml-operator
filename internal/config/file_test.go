package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadFileYAML(t *testing.T) {
	path := writeConfig(t, ""+
		"# operator config\n"+
		"namespace: ml-models\n"+
		"metricsAddr: 127.0.0.1:9090\n"+
		"reconcileIntervalSeconds: 60\n"+
		"applyMaxAttempts: 5\n"+
		"applyRetryBackoffMs: 250\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Namespace != "ml-models" {
		t.Errorf("namespace = %q, want %q", cfg.Namespace, "ml-models")
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("metricsAddr = %q, want %q", cfg.MetricsAddr, "127.0.0.1:9090")
	}
	if cfg.ReconcileIntervalSeconds != 60 {
		t.Errorf("reconcileIntervalSeconds = %d, want 60", cfg.ReconcileIntervalSeconds)
	}
	if cfg.ApplyMaxAttempts != 5 {
		t.Errorf("applyMaxAttempts = %d, want 5", cfg.ApplyMaxAttempts)
	}
	if cfg.ApplyRetryBackoffMs != 250 {
		t.Errorf("applyRetryBackoffMs = %d, want 250", cfg.ApplyRetryBackoffMs)
	}
}

func TestLoadFileJSONAndPartialKeepsDefaults(t *testing.T) {
	path := writeConfig(t, `{"namespace": "staging", "reconcileIntervalSeconds": 0}`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Namespace != "staging" {
		t.Errorf("namespace = %q, want %q", cfg.Namespace, "staging")
	}
	if cfg.ReconcileIntervalSeconds != 0 {
		t.Errorf("reconcileIntervalSeconds = %d, want 0", cfg.ReconcileIntervalSeconds)
	}
	def := Defaults()
	if cfg.MetricsAddr != def.MetricsAddr || cfg.ApplyMaxAttempts != def.ApplyMaxAttempts || cfg.ApplyRetryBackoffMs != def.ApplyRetryBackoffMs {
		t.Errorf("absent keys should keep defaults, got %+v", cfg)
	}
}

func TestLoadFileErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"unknown key", "namespace: x\nbogus: 1\n", "unknown key"},
		{"wrong type", "namespace: 5\n", "must be a string"},
		{"fractional int", "applyMaxAttempts: 2.5\n", "whole number"},
		{"empty file", "", "empty"},
		{"bad json", "{not json", "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.content)
			if _, err := LoadFile(path); err == nil {
				t.Fatalf("expected an error, got none")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "no-such.yaml")); err == nil {
		t.Fatalf("expected an error for a missing file, got none")
	}
}
