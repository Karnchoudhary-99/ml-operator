package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/rollout"
)

// Version is the state-file format version. Load refuses a newer
// version instead of guessing at fields it does not know.
const Version = 1

// State is everything the mlop CLI persists between invocations.
type State struct {
	Version int `json:"version"`
	// Deployments are the registered ModelDeployments, status included.
	Deployments []api.ModelDeployment `json:"deployments"`
	// Cluster is the simulated cluster: rendered objects, readiness,
	// and load observations.
	Cluster controller.ClusterSnapshot `json:"cluster"`
	// Rollouts is the per-deployment canary rollout state.
	Rollouts map[string]rollout.State `json:"rollouts"`
	// Events is the recorded operator event history, newest last
	// (the recorder reverses it for display).
	Events []metrics.Event `json:"events"`
}

// Runtime is the live in-memory world rebuilt from a State: the
// registry, the fake cluster, and the reconciler wired over both.
type Runtime struct {
	Registry   *registry.Registry
	Cluster    *controller.FakeCluster
	Reconciler *controller.Reconciler
}

// DefaultPath returns the state file path: ~/.mlop/state.json, or
// $MLOP_STATE when set (tests and scripts use that).
func DefaultPath() string {
	if p := os.Getenv("MLOP_STATE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".mlop-state.json"
	}
	return filepath.Join(home, ".mlop", "state.json")
}

// Load reads a state file. A missing file is not an error: it means
// the CLI has never run, so an empty state is returned.
func Load(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &State{Version: Version}, nil
		}
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	if s.Version > Version {
		return nil, fmt.Errorf("state file %s is version %d, this mlop only reads up to %d", path, s.Version, Version)
	}
	if s.Version == 0 {
		s.Version = Version
	}
	return &s, nil
}

// Save writes the state atomically (temp file + rename) so a crash
// mid-write never leaves a half-written state file behind.
func Save(path string, s *State) error {
	if s.Version == 0 {
		s.Version = Version
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".mlop-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod state file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("publish state file %s: %w", path, err)
	}
	return nil
}

// From builds a State from the live runtime objects.
func From(reg *registry.Registry, cluster *controller.FakeCluster, rec *controller.Reconciler) *State {
	s := &State{Version: Version}
	for _, e := range reg.List() {
		s.Deployments = append(s.Deployments, e.Deployment)
	}
	s.Cluster = cluster.Snapshot()
	s.Rollouts = rec.RolloutStates()
	s.Events = rec.Events.List()
	// Events.List returns newest first; store oldest first so the
	// file reads chronologically.
	for i, j := 0, len(s.Events)-1; i < j; i, j = i+1, j-1 {
		s.Events[i], s.Events[j] = s.Events[j], s.Events[i]
	}
	return s
}

// Restore rebuilds the live runtime from a loaded State. Deployments
// are re-registered with their status (a fresh registry has no
// entries, so Register inserts the whole deployment, status
// included). Rollout states that no longer match the deployment
// (new generation, changed ladder) are dropped by the reconciler.
func (s *State) Restore() *Runtime {
	reg := registry.New()
	for _, d := range s.Deployments {
		reg.Register(d)
	}
	cluster := controller.NewFakeCluster()
	cluster.Restore(s.Cluster)
	rec := controller.New(reg, cluster)
	for key, rs := range s.Rollouts {
		rec.RestoreRolloutState(key, rs)
	}
	rec.Events.Restore(s.Events)
	return &Runtime{Registry: reg, Cluster: cluster, Reconciler: rec}
}
