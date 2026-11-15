package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

func writeState(t *testing.T, s *store.State) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.Save(path, s); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return path
}

func sampleState() *store.State {
	replicas := int32(2)
	return &store.State{
		Version: store.Version,
		Deployments: []api.ModelDeployment{
			{
				Name:       "iris-mlp",
				Namespace:  "default",
				Generation: 1,
				Spec: api.ModelDeploymentSpec{
					Model:    api.ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
					Replicas: &replicas,
					Port:     8080,
					Strategy: api.StrategyRolling,
				},
			},
		},
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")

	summary, err := Write(statePath, backupPath, "0.1.0", false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if summary.Deployments != 1 {
		t.Fatalf("summary deployments = %d, want 1", summary.Deployments)
	}
	if summary.MlopVersion != "0.1.0" {
		t.Fatalf("summary mlop version = %q, want 0.1.0", summary.MlopVersion)
	}
	if len(summary.SHA256) != 64 {
		t.Fatalf("summary checksum = %q, want 64 hex chars", summary.SHA256)
	}
	if summary.ShortChecksum() != summary.SHA256[:12] {
		t.Fatalf("short checksum = %q, want %q", summary.ShortChecksum(), summary.SHA256[:12])
	}
	if summary.Bytes <= 0 {
		t.Fatalf("summary bytes = %d, want > 0", summary.Bytes)
	}

	f, readSummary, err := Read(backupPath)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.Version != Version {
		t.Fatalf("backup version = %d, want %d", f.Version, Version)
	}
	if readSummary.Deployments != 1 || readSummary.SHA256 != summary.SHA256 {
		t.Fatalf("read summary %+v, want deployments 1 and checksum %s", readSummary, summary.SHA256)
	}
	var st store.State
	if err := json.Unmarshal(f.State, &st); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(st.Deployments) != 1 || st.Deployments[0].Spec.Model.Image != "registry.example.com/iris-mlp:v1" {
		t.Fatalf("restored payload deployments = %+v", st.Deployments)
	}
}

func TestWriteRefusesOverwriteWithoutForce(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := Write(statePath, backupPath, "0.1.0", false); err == nil {
		t.Fatal("second Write without --force succeeded, want refusal")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second Write error %q, want 'already exists'", err)
	}
	if _, err := Write(statePath, backupPath, "0.1.0", true); err != nil {
		t.Fatalf("Write with force: %v", err)
	}
}

func TestWriteMissingStateFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-state.json")
	if _, err := Write(missing, filepath.Join(t.TempDir(), "b.json"), "0.1.0", false); err == nil {
		t.Fatal("Write of a missing state file succeeded, want an error")
	} else if !strings.Contains(err.Error(), "nothing to back up") {
		t.Fatalf("Write error %q, want 'nothing to back up'", err)
	}
}

func TestWriteRejectsInvalidState(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(bad, filepath.Join(t.TempDir(), "b.json"), "0.1.0", false); err == nil {
		t.Fatal("Write of corrupt state succeeded, want an error")
	}
}

func TestReadRejectsTamperedPayload(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	// Flip one character inside the embedded state payload: the
	// image tag v1 -> v2 changes the payload without touching the
	// stored checksum, so Read must catch it.
	tampered := strings.Replace(string(raw), "iris-mlp:v1", "iris-mlp:v2", 1)
	if tampered == string(raw) {
		t.Fatal("test setup: payload string not found in backup file")
	}
	if err := os.WriteFile(backupPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(backupPath); err == nil {
		t.Fatal("Read of a tampered backup succeeded, want a checksum error")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Read error %q, want 'checksum mismatch'", err)
	}
}

func TestReadRejectsFutureVersion(t *testing.T) {
	statePath := writeState(t, sampleState())
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if _, err := Write(statePath, backupPath, "0.1.0", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Version = Version + 1
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(backupPath); err == nil {
		t.Fatal("Read of a future-version backup succeeded, want an error")
	} else if !strings.Contains(err.Error(), "version") {
		t.Fatalf("Read error %q, want a version error", err)
	}
}

func TestReadMissingFile(t *testing.T) {
	if _, _, err := Read(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("Read of a missing backup succeeded, want an error")
	} else if !strings.Contains(err.Error(), "no backup file") {
		t.Fatalf("Read error %q, want 'no backup file'", err)
	}
}
