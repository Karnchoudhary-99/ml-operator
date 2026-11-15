package registry

import (
	"sort"
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
