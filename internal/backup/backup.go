package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// Version is the backup-file format version. Read refuses a newer
// version instead of guessing at fields it does not know.
const Version = 1

// File is a backup envelope: the raw state-file bytes plus the
// metadata needed to trust them before a restore.
type File struct {
	Version     int             `json:"version"`
	CreatedAt   time.Time       `json:"createdAt"`
	MlopVersion string          `json:"mlopVersion"`
	Deployments int             `json:"deployments"`
	SHA256      string          `json:"sha256"`
	State       json.RawMessage `json:"state"`
}

// Summary is the human-facing description of a backup: what it
// holds and when it was taken. Write, Read and Restore all return
// one, so the CLI prints the same shape for every operation.
type Summary struct {
	Path        string
	CreatedAt   time.Time
	MlopVersion string
	Deployments int
	SHA256      string
	Bytes       int64
}

// ShortChecksum is the first 12 hex characters of the payload
// checksum — enough to identify a backup in CLI output.
func (s *Summary) ShortChecksum() string {
	if len(s.SHA256) > 12 {
		return s.SHA256[:12]
	}
	return s.SHA256
}

// Write snapshots the state file at statePath into a new backup
// file at backupPath. It refuses to overwrite an existing backup
// unless force is set — a backup is the last copy, overwriting it
// by accident should be a deliberate act.
func Write(statePath, backupPath, mlopVersion string, force bool) (*Summary, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no state file at %s: nothing to back up (apply a deployment first)", statePath)
		}
		return nil, fmt.Errorf("read state file %s: %w", statePath, err)
	}
	var st store.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("state file %s is not valid mlop state: %w", statePath, err)
	}
	if st.Version > store.Version {
		return nil, fmt.Errorf("state file %s is version %d, this mlop only reads up to %d", statePath, st.Version, store.Version)
	}
	if !force {
		if _, err := os.Stat(backupPath); err == nil {
			return nil, fmt.Errorf("backup file %s already exists (use --force to overwrite it)", backupPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("check backup file %s: %w", backupPath, err)
		}
	}

	if st.Version == 0 {
		st.Version = store.Version
	}
	// Canonicalise the payload before hashing it: the envelope is
	// written indented, which re-formats embedded raw JSON, so the
	// checksum must be over the compact form — and Read compacts
	// the stored payload the same way before verifying.
	canonical, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	sum := sha256.Sum256(canonical)
	f := &File{
		Version:     Version,
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		MlopVersion: mlopVersion,
		Deployments: len(st.Deployments),
		SHA256:      hex.EncodeToString(sum[:]),
		State:       json.RawMessage(canonical),
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup: %w", err)
	}
	if err := writeAtomic(backupPath, out); err != nil {
		return nil, err
	}
	fi, err := os.Stat(backupPath)
	if err != nil {
		return nil, fmt.Errorf("stat backup file %s: %w", backupPath, err)
	}
	return &Summary{
		Path:        backupPath,
		CreatedAt:   f.CreatedAt,
		MlopVersion: f.MlopVersion,
		Deployments: f.Deployments,
		SHA256:      f.SHA256,
		Bytes:       fi.Size(),
	}, nil
}

// Read loads a backup file and verifies it: the format version
// must be readable, the SHA-256 must match the payload, and the
// payload must parse as mlop state. A backup that fails any of
// these is unusable, and Read says so instead of handing a
// corrupt world to a restore.
func Read(backupPath string) (*File, *Summary, error) {
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("no backup file at %s", backupPath)
		}
		return nil, nil, fmt.Errorf("read backup file %s: %w", backupPath, err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, fmt.Errorf("backup file %s is not a valid backup: %w", backupPath, err)
	}
	if f.Version > Version {
		return nil, nil, fmt.Errorf("backup file %s is version %d, this mlop only reads up to %d", backupPath, f.Version, Version)
	}
	if f.Version == 0 {
		return nil, nil, fmt.Errorf("backup file %s has no format version: not a backup written by mlop", backupPath)
	}
	// Verify the checksum over the canonical (compacted) payload:
	// the envelope file is indented, which re-formats the embedded
	// state, so the bytes on disk are not the bytes that were
	// hashed at write time.
	var compact bytes.Buffer
	if err := json.Compact(&compact, f.State); err != nil {
		return nil, nil, fmt.Errorf("backup file %s holds invalid state: %w", backupPath, err)
	}
	sum := sha256.Sum256(compact.Bytes())
	if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
		return nil, nil, fmt.Errorf("backup file %s is corrupt: checksum mismatch (stored %s, computed %s)", backupPath, shortHash(f.SHA256), shortHash(got))
	}
	var st store.State
	if err := json.Unmarshal(f.State, &st); err != nil {
		return nil, nil, fmt.Errorf("backup file %s holds invalid state: %w", backupPath, err)
	}
	if st.Version > store.Version {
		return nil, nil, fmt.Errorf("backup file %s holds state version %d, this mlop only reads up to %d", backupPath, st.Version, store.Version)
	}
	fi, err := os.Stat(backupPath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat backup file %s: %w", backupPath, err)
	}
	summary := &Summary{
		Path:        backupPath,
		CreatedAt:   f.CreatedAt,
		MlopVersion: f.MlopVersion,
		Deployments: f.Deployments,
		SHA256:      f.SHA256,
		Bytes:       fi.Size(),
	}
	// The header count is advisory; the payload is authoritative.
	summary.Deployments = len(st.Deployments)
	return &f, summary, nil
}

// Restore verifies a backup and writes its state into the state
// file at statePath. When the live state already holds deployments
// and force is false, it refuses: restoring over a live world
// silently would drop every deployment the backup does not know
// about. Pass force to say the overwrite is intended.
func Restore(backupPath, statePath string, force bool) (*Summary, error) {
	f, summary, err := Read(backupPath)
	if err != nil {
		return nil, err
	}
	if !force {
		if current, err := store.Load(statePath); err != nil {
			return nil, fmt.Errorf("read current state %s: %w", statePath, err)
		} else if len(current.Deployments) > 0 {
			return nil, fmt.Errorf("state file %s already holds %d deployment(s); refusing to overwrite it (use --force to restore anyway)", statePath, len(current.Deployments))
		}
	}
	var st store.State
	if err := json.Unmarshal(f.State, &st); err != nil {
		return nil, fmt.Errorf("backup file %s holds invalid state: %w", backupPath, err)
	}
	if err := store.Save(statePath, &st); err != nil {
		return nil, fmt.Errorf("write state file %s: %w", statePath, err)
	}
	return summary, nil
}

// writeAtomic writes data to path via a temp file + rename, so a
// crash mid-write never leaves a half-written backup behind.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".mlop-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp backup file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write backup file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close backup file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod backup file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("publish backup file %s: %w", path, err)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
