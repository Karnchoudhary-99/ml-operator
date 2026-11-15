package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRestoreFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	manifest := writeManifest(t, irisV1)

	// Build a real world: apply v1, bring its pods up.
	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	code, _, _ = runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2")
	if code != 0 {
		t.Fatalf("ready exit %d", code)
	}

	// Back it up.
	code, out, errOut := runCLIState(t, state, "backup", backupPath)
	if code != 0 {
		t.Fatalf("backup exit %d, out %q err %q", code, out, errOut)
	}
	if !strings.Contains(out, "backed up 1 deployment(s)") || !strings.Contains(out, "checksum:") {
		t.Fatalf("backup output %q", out)
	}

	// A second backup to the same file needs --force.
	code, _, errOut = runCLIState(t, state, "backup", backupPath)
	if code == 0 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second backup exit %d, err %q, want 'already exists'", code, errOut)
	}
	code, out, _ = runCLIState(t, state, "backup", "--force", backupPath)
	if code != 0 || !strings.Contains(out, "backed up 1 deployment(s)") {
		t.Fatalf("forced backup exit %d, out %q", code, out)
	}

	// Restoring over the live state is refused without --force.
	code, _, errOut = runCLIState(t, state, "restore", backupPath)
	if code == 0 || !strings.Contains(errOut, "already holds") {
		t.Fatalf("restore over live state exit %d, err %q, want 'already holds'", code, errOut)
	}

	// Wipe the state file, restore, and the world comes back:
	// deployment Available (pods were ready) with its history.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCLIState(t, state, "restore", backupPath)
	if code != 0 || !strings.Contains(out, "restored 1 deployment(s)") {
		t.Fatalf("restore exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "Available") || !strings.Contains(out, "2/2") {
		t.Fatalf("status after restore exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "history", "iris-mlp")
	if code != 0 || !strings.Contains(out, "Available") {
		t.Fatalf("history after restore exit %d, out %q", code, out)
	}
}

func TestBackupRestoreErrors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	backupPath := filepath.Join(t.TempDir(), "backup.json")

	// Nothing applied yet: there is no state to back up.
	code, _, errOut := runCLIState(t, state, "backup", backupPath)
	if code == 0 || !strings.Contains(errOut, "nothing to back up") {
		t.Fatalf("backup with no state exit %d, err %q", code, errOut)
	}

	// Restoring a missing backup file fails cleanly.
	code, _, errOut = runCLIState(t, state, "restore", backupPath)
	if code == 0 || !strings.Contains(errOut, "no backup file") {
		t.Fatalf("restore of missing backup exit %d, err %q", code, errOut)
	}

	// Build a backup, tamper with its payload, and the restore
	// must refuse it — and leave no state file behind.
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatal("apply failed in test setup")
	}
	if code, _, _ := runCLIState(t, state, "backup", backupPath); code != 0 {
		t.Fatal("backup failed in test setup")
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "iris-mlp:v1", "iris-mlp:v2", 1)
	if err := os.WriteFile(backupPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	wiped := filepath.Join(t.TempDir(), "wiped.json")
	code, _, errOut = runCLIState(t, wiped, "restore", backupPath)
	if code == 0 || !strings.Contains(errOut, "checksum mismatch") {
		t.Fatalf("restore of tampered backup exit %d, err %q", code, errOut)
	}
	if _, err := os.Stat(wiped); !os.IsNotExist(err) {
		t.Fatal("failed restore still wrote a state file")
	}

	// Both commands require exactly one path argument.
	code, _, errOut = runCLIState(t, state, "backup")
	if code == 0 || !strings.Contains(errOut, "exactly one backup file path") {
		t.Fatalf("backup with no path exit %d, err %q", code, errOut)
	}
	code, _, errOut = runCLIState(t, state, "restore")
	if code == 0 || !strings.Contains(errOut, "exactly one backup file path") {
		t.Fatalf("restore with no path exit %d, err %q", code, errOut)
	}
}
