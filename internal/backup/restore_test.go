package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

func TestRestoreRoundTrip(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Wipe the live state entirely, then restore it from the backup.
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	summary, err := Restore(backupPath, statePath, false)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if summary.Deployments != 1 {
		t.Fatalf("restore summary deployments = %d, want 1", summary.Deployments)
	}
	restored, err := store.Load(statePath)
	if err != nil {
		t.Fatalf("load restored state: %v", err)
	}
	if len(restored.Deployments) != 1 || restored.Deployments[0].Spec.Model.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("restored deployments = %+v", restored.Deployments)
	}
}

func TestRestoreRefusesNonEmptyState(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The live state still holds the deployment: a plain restore
	// must refuse rather than silently clobber it.
	if _, err := Restore(backupPath, statePath, false); err == nil {
		t.Fatal("Restore over a non-empty state succeeded, want refusal")
	} else if !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("Restore error %q, want 'already holds'", err)
	}
	// --force says the overwrite is intended, and it works.
	if _, err := Restore(backupPath, statePath, true); err != nil {
		t.Fatalf("Restore with force: %v", err)
	}
}

func TestRestoreAllowsEmptyState(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// An empty (zero-deployment) state file is not a live world,
	// so restoring over it needs no --force.
	emptyPath := writeState(t, &store.State{Version: store.Version})
	if _, err := Restore(backupPath, emptyPath, false); err != nil {
		t.Fatalf("Restore over empty state: %v", err)
	}
	restored, err := store.Load(emptyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Deployments) != 1 {
		t.Fatalf("restored deployments = %d, want 1", len(restored.Deployments))
	}
}

func TestRestoreRejectsTamperedBackup(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "iris-mlp:v1", "iris-mlp:v2", 1)
	if err := os.WriteFile(backupPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "state.json")
	if _, err := Restore(backupPath, target, false); err == nil {
		t.Fatal("Restore of a tampered backup succeeded, want a checksum error")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Restore error %q, want 'checksum mismatch'", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("failed restore still wrote a state file")
	}
}
