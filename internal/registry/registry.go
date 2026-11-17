package registry

import (
	"sort"
	"strings"
	"sync"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// EventType classifies a registry change notification.
type EventType string

const (
	// EventAdded fires when a deployment is first registered.
	EventAdded EventType = "Added"
	// EventModified fires when an existing deployment is re-registered
	// with a new generation (or any changed spec).
	EventModified EventType = "Modified"
	// EventDeleted fires when a deployment is removed.
	EventDeleted EventType = "Deleted"
)

// Event is a single registry change notification.
type Event struct {
	Type       EventType
	Deployment api.ModelDeployment
	// Revision is the registry revision after this event.
	Revision int64
}

// Entry is one registered deployment plus its registry metadata.
type Entry struct {
	Deployment api.ModelDeployment
	// Revision counts registrations of this key (starts at 1).
	Revision int64
}

// Registry is a thread-safe in-memory store of ModelDeployments. The
// operator's reconciler reads from it; the API server (and the CLI in
// demo mode) writes to it. It is the source of truth until the
// Kubernetes-backed store lands.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]*Entry
	revision int64 // global revision, bumped on every mutation
	watchers map[int]chan Event
	nextID   int
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		entries:  make(map[string]*Entry),
		watchers: make(map[int]chan Event),
	}
}

// Register inserts a new deployment or updates an existing one. It
// returns the entry and whether the key already existed.
// Re-registering an identical deployment (same spec and generation)
// is a no-op: no revision bump, no event. Status-only changes do NOT
// go through Register (they would be dropped by the no-op check) —
// use UpdateStatus for those.
func (r *Registry) Register(d api.ModelDeployment) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := d.Key()
	existing, ok := r.entries[key]
	if ok {
		// Identical spec and generation: a true no-op — no revision
		// bump, no event. Re-registration is idempotent.
		if SpecsEqual(existing.Deployment.Spec, d.Spec) &&
			existing.Deployment.Generation == d.Generation {
			snapshot := *existing
			return &snapshot, true
		}
		// Status is owned by the reconciler, not by whoever
		// registers a spec: a re-registration carries a zero
		// status (a parsed manifest has none), and overwriting
		// it would wipe the phase, the conditions, and the
		// rollback target. Keep the stored status; only the spec
		// and generation move. (Inserting a new key keeps the
		// status as given — Restore re-registers deployments
		// from the state file with their status included.)
		d.Status = existing.Deployment.Status
		existing.Deployment = d
		existing.Revision++
	} else {
		existing = &Entry{Deployment: d, Revision: 1}
		r.entries[key] = existing
	}
	r.revision++
	snapshot := *existing
	typ := EventAdded
	if ok {
		typ = EventModified
	}
	rev := r.revision
	r.notifyLocked(Event{Type: typ, Deployment: snapshot.Deployment, Revision: rev}, true)
	return &snapshot, ok
}

// Get returns the entry for key ("namespace/name"), or nil.
func (r *Registry) Get(key string) *Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[key]; ok {
		snapshot := *e
		return &snapshot
	}
	return nil
}

// List returns all entries sorted by key.
func (r *Registry) List() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*Entry, 0, len(keys))
	for _, k := range keys {
		snapshot := *r.entries[k]
		out = append(out, &snapshot)
	}
	return out
}

// UpdateStatus replaces the status of the deployment at key without
// touching its spec or generation, and returns false when the key is
// not present. A status write bumps the global revision and emits a
// Modified event, so watchers (including the reconciler's status
// loop) observe phase transitions. It is the only way to write
// status: Register ignores status-only re-registrations by design.
func (r *Registry) UpdateStatus(key string, status api.ModelDeploymentStatus) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key]
	if !ok {
		return false
	}
	e.Deployment.Status = status
	r.revision++
	snapshot := *e
	r.notifyLocked(Event{Type: EventModified, Deployment: snapshot.Deployment, Revision: r.revision}, true)
	return true
}

// Delete removes the entry for key. It returns the removed entry, or
// nil when the key was not present.
func (r *Registry) Delete(key string) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key]
	if !ok {
		return nil
	}
	delete(r.entries, key)
	r.revision++
	snapshot := *e
	r.notifyLocked(Event{Type: EventDeleted, Deployment: snapshot.Deployment, Revision: r.revision}, true)
	return &snapshot
}

// Len returns the number of registered deployments.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Revision returns the global revision (number of mutations so far).
func (r *Registry) Revision() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision
}

// Watch returns a channel receiving every registry event from now on,
// plus an unsubscribe function. The channel is buffered (16 events);
// a slow watcher drops the oldest event rather than blocking the
// registry — watchers must re-list after a gap.
func (r *Registry) Watch() (<-chan Event, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan Event, 16)
	id := r.nextID
	r.nextID++
	r.watchers[id] = ch
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.watchers, id)
	}
}

// notifyLocked broadcasts an event. Callers must hold r.mu. The
// changed flag is always true at the call sites; it exists so a
// future caller can skip no-op notifications.
func (r *Registry) notifyLocked(ev Event, changed bool) {
	if !changed {
		return
	}
	for _, ch := range r.watchers {
		select {
		case ch <- ev:
		default:
			// Slow watcher: drop the oldest event to make room.
			select {
			case <-ch:
			default:
			}
			ch <- ev
		}
	}
}

// SpecsEqual reports whether two specs are identical. It compares the
// fields the reconciler cares about; Env maps are compared by content.
// The mlop CLI uses it too, to decide whether an apply changed anything.
func SpecsEqual(a, b api.ModelDeploymentSpec) bool {
	if a.Model != b.Model || a.Resources != b.Resources || a.Health != b.Health {
		return false
	}
	if (a.Replicas == nil) != (b.Replicas == nil) {
		return false
	}
	if a.Replicas != nil && *a.Replicas != *b.Replicas {
		return false
	}
	if a.Port != b.Port || a.Strategy != b.Strategy {
		return false
	}
	// Pausing is a spec change: toggling it must bump the
	// generation, or `mlop apply` of a paused manifest would
	// call it unchanged and the reconciler would never see
	// the new value.
	if a.Paused != b.Paused {
		return false
	}
	// The maintenance window is a spec change for the same
	// reason: changing the days or hours must bump the
	// generation, or `mlop apply` of a re-windowed manifest
	// would call it unchanged and the reconciler would keep
	// deferring (or applying) under the old window.
	if (a.MaintenanceWindow == nil) != (b.MaintenanceWindow == nil) {
		return false
	}
	if a.MaintenanceWindow != nil && !windowEqual(*a.MaintenanceWindow, *b.MaintenanceWindow) {
		return false
	}
	if (a.Canary == nil) != (b.Canary == nil) {
		return false
	}
	if a.Canary != nil && !canaryEqual(*a.Canary, *b.Canary) {
		return false
	}
	if (a.BlueGreen == nil) != (b.BlueGreen == nil) {
		return false
	}
	if a.BlueGreen != nil && !gateEqual(a.BlueGreen.Gate, b.BlueGreen.Gate) {
		return false
	}
	if (a.Autoscaling == nil) != (b.Autoscaling == nil) {
		return false
	}
	if a.Autoscaling != nil && *a.Autoscaling != *b.Autoscaling {
		return false
	}
	if (a.Availability == nil) != (b.Availability == nil) {
		return false
	}
	if a.Availability != nil && !availabilityEqual(*a.Availability, *b.Availability) {
		return false
	}
	if (a.Security == nil) != (b.Security == nil) {
		return false
	}
	if a.Security != nil && !securityEqual(*a.Security, *b.Security) {
		return false
	}
	// Placement is a spec change for the same reason: moving a
	// model to different nodes must bump the generation, or
	// `mlop apply` of a re-placed manifest would call it
	// unchanged and the reconciler would keep serving from the
	// old nodes.
	if (a.Placement == nil) != (b.Placement == nil) {
		return false
	}
	if a.Placement != nil && !placementEqual(*a.Placement, *b.Placement) {
		return false
	}
	// A model source is a spec change for the same reason:
	// pointing the deployment at a different artifact must
	// bump the generation, or `mlop apply` of a re-sourced
	// manifest would call it unchanged and the reconciler
	// would keep serving the old artifact. The source
	// compares by value: it is a plain struct of strings,
	// so a JSON round-trip through the state file still
	// equals a freshly parsed manifest.
	if (a.ModelSource == nil) != (b.ModelSource == nil) {
		return false
	}
	if a.ModelSource != nil && *a.ModelSource != *b.ModelSource {
		return false
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if bv, ok := b.Env[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// windowEqual compares two maintenance windows by value. Days
// compare as a set, case-insensitively: ["Mon", "Wed"] and
// ["wed", "Mon"] are the same window, so re-ordering the days in
// a manifest must not bump the generation.
func windowEqual(a, b api.MaintenanceWindowSpec) bool {
	if a.Start != b.Start || a.End != b.End || len(a.Days) != len(b.Days) {
		return false
	}
	for _, d := range a.Days {
		found := false
		for _, e := range b.Days {
			if strings.EqualFold(d, e) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// canaryEqual compares two canary specs by value. A plain struct
// comparison would compare the Gate pointers, not the thresholds,
// so a spec that survived a JSON round-trip (state file) would
// never equal a freshly parsed manifest with the same gate —
// `mlop diff -f` would bump the generation on every identical
// re-apply instead of reporting in sync.
func canaryEqual(a, b api.CanarySpec) bool {
	if a.Weight != b.Weight || a.Steps != b.Steps || a.IntervalSeconds != b.IntervalSeconds {
		return false
	}
	return gateEqual(a.Gate, b.Gate)
}

// gateEqual compares two gate specs by value: nil vs set differs,
// equal thresholds are equal whatever the pointers are.
func gateEqual(a, b *api.GateSpec) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

// securityEqual compares two security specs by value: nil vs set
// differs, equal fields are equal whatever the pointers are. The
// pointer booleans and uid are compared by value, and the
// capability list by content — a spec that survived a JSON
// round-trip (state file) must still equal a freshly parsed
// manifest with the same security, or `mlop diff -f` would bump
// the generation on every identical re-apply.
func securityEqual(a, b api.SecuritySpec) bool {
	if !boolPtrEqual(a.RunAsNonRoot, b.RunAsNonRoot) ||
		!int64PtrEqual(a.RunAsUser, b.RunAsUser) ||
		!boolPtrEqual(a.ReadOnlyRootFilesystem, b.ReadOnlyRootFilesystem) ||
		!boolPtrEqual(a.AllowPrivilegeEscalation, b.AllowPrivilegeEscalation) {
		return false
	}
	if len(a.DropCapabilities) != len(b.DropCapabilities) {
		return false
	}
	for i := range a.DropCapabilities {
		if a.DropCapabilities[i] != b.DropCapabilities[i] {
			return false
		}
	}
	return a.SeccompProfile == b.SeccompProfile
}

// boolPtrEqual compares two optional booleans by value.
func boolPtrEqual(a, b *bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

// int64PtrEqual compares two optional int64s by value.
func int64PtrEqual(a, b *int64) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

// availabilityEqual compares two availability specs by value: nil
// vs set differs, equal fields are equal whatever the pointers
// are. The anti-affinity flag is a pointer, so a plain struct
// comparison would compare addresses — a spec that survived a
// JSON round-trip (state file) would never equal a freshly parsed
// manifest with the same flag, and `mlop diff -f` would bump the
// generation on every identical re-apply instead of reporting in
// sync.
func availabilityEqual(a, b api.AvailabilitySpec) bool {
	if a.MinAvailable != b.MinAvailable || a.MaxSkew != b.MaxSkew {
		return false
	}
	if (a.AntiAffinity == nil) != (b.AntiAffinity == nil) {
		return false
	}
	if a.AntiAffinity != nil && *a.AntiAffinity != *b.AntiAffinity {
		return false
	}
	return true
}

// placementEqual compares two placement specs by value: the
// nodeSelector by content (map order is meaningless) and the
// tolerations by value, in order — including the seconds, which
// are pointers. A spec that survived a JSON round-trip (state
// file) must still equal a freshly parsed manifest with the same
// placement, or `mlop diff -f` would bump the generation on
// every identical re-apply.
func placementEqual(a, b api.PlacementSpec) bool {
	if len(a.NodeSelector) != len(b.NodeSelector) {
		return false
	}
	for k, v := range a.NodeSelector {
		if bv, ok := b.NodeSelector[k]; !ok || bv != v {
			return false
		}
	}
	if len(a.Tolerations) != len(b.Tolerations) {
		return false
	}
	for i := range a.Tolerations {
		if a.Tolerations[i].Key != b.Tolerations[i].Key ||
			a.Tolerations[i].Operator != b.Tolerations[i].Operator ||
			a.Tolerations[i].Value != b.Tolerations[i].Value ||
			a.Tolerations[i].Effect != b.Tolerations[i].Effect {
			return false
		}
		if (a.Tolerations[i].TolerationSeconds == nil) != (b.Tolerations[i].TolerationSeconds == nil) {
			return false
		}
		if a.Tolerations[i].TolerationSeconds != nil &&
			*a.Tolerations[i].TolerationSeconds != *b.Tolerations[i].TolerationSeconds {
			return false
		}
	}
	return true
}
