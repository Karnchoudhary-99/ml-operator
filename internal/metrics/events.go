package metrics

import (
	"sync"
	"time"
)

// EventType classifies a recorded event, following the Kubernetes
// convention: Normal for expected transitions, Warning for problems.
type EventType string

const (
	// EventNormal marks an expected transition (scaled up, rollout
	// promoted, deployment available).
	EventNormal EventType = "Normal"
	// EventWarning marks a problem (rollout aborted, reconcile
	// error, invalid spec).
	EventWarning EventType = "Warning"
)

// DefaultMaxEvents caps the recorder's ring buffer. Old events drop
// off the front; the newest events are always kept.
const DefaultMaxEvents = 100

// Event is one recorded operator event: what happened, to which
// deployment, and when.
type Event struct {
	// Deployment is the "namespace/name" the event is about.
	Deployment string
	Type       EventType
	Reason     string // short CamelCase reason, e.g. "ScaledUp"
	Message    string // human-readable detail
	Count      int    // how many times this identical event recurred
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Recorder keeps a bounded, deduplicated ring of events. Recording
// the same deployment/reason/message again bumps that event's
// Count and LastSeen instead of adding a duplicate — the same
// aggregation Kubernetes does for repeated events.
type Recorder struct {
	mu     sync.Mutex
	max    int
	events []Event
	// Now is the clock; nil means time.Now. Tests set a
	// controllable clock.
	Now func() time.Time
}

// NewRecorder returns a Recorder capped at max events (0 means the
// default of 100).
func NewRecorder(max int) *Recorder {
	if max <= 0 {
		max = DefaultMaxEvents
	}
	return &Recorder{max: max}
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Record adds an event, aggregating it with an identical one when
// present. Identical means the same deployment, type, reason and
// message.
func (r *Recorder) Record(deployment string, typ EventType, reason, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for i := range r.events {
		e := &r.events[i]
		if e.Deployment == deployment && e.Type == typ && e.Reason == reason && e.Message == message {
			e.Count++
			e.LastSeen = now
			return
		}
	}
	r.events = append(r.events, Event{
		Deployment: deployment,
		Type:       typ,
		Reason:     reason,
		Message:    message,
		Count:      1,
		FirstSeen:  now,
		LastSeen:   now,
	})
	if len(r.events) > r.max {
		r.events = r.events[len(r.events)-r.max:]
	}
}

// List returns the recorded events, newest first.
func (r *Recorder) List() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	for i, e := range r.events {
		out[len(r.events)-1-i] = e
	}
	return out
}

// EventsFor returns the recorded events for one deployment, newest
// first.
func (r *Recorder) EventsFor(deployment string) []Event {
	all := r.List()
	var out []Event
	for _, e := range all {
		if e.Deployment == deployment {
			out = append(out, e)
		}
	}
	return out
}

// Len returns how many events are currently recorded.
func (r *Recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}
