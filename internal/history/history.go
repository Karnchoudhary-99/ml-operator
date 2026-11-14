package history

import (
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// MaxEntries caps one deployment's history. A deployment that
// reconciles forever (autoscaling, repeated rollouts) must not grow
// the state file without bound; the oldest entries drop first, so
// the log always holds the most recent transitions.
const MaxEntries = 50

// Entry is one recorded transition in a deployment's life.
type Entry struct {
	// Generation is the deployment generation this transition
	// belongs to: a new generation starts a new version, a
	// rollback, or a spec change.
	Generation int64 `json:"generation"`
	// Image is the model image the spec asked for at this point
	// (the desired image, not necessarily the one serving yet —
	// during a canary the stable Deployment still serves the
	// previous image while the history already shows the new one).
	Image string `json:"image"`
	// Version is the model version label for the image.
	Version string `json:"version"`
	// Phase is the phase the reconciler recorded after this
	// transition (Pending, Progressing, Available, Degraded,
	// Failed).
	Phase api.Phase `json:"phase"`
	// Reason is the reconciler's message for the transition, e.g.
	// "2/2 replicas ready" or "canary promoted at 100%".
	Reason string `json:"reason"`
	// At is when the transition was recorded (reconciler clock).
	At time.Time `json:"at"`
}

// SameTransition reports whether e records the same transition as
// the tuple the reconciler is about to record: same generation,
// same desired image and version, same phase. The reconciler only
// appends when this is false, so a steady deployment that
// reconciles every few seconds does not spam its own history —
// only real transitions are recorded.
func (e Entry) SameTransition(generation int64, image, version string, phase api.Phase) bool {
	return e.Generation == generation && e.Image == image &&
		e.Version == version && e.Phase == phase
}

// Log is the bounded history for one deployment: entries in
// chronological order, oldest first, at most MaxEntries long.
type Log struct {
	entries []Entry
}

// New returns an empty log.
func New() *Log {
	return &Log{}
}

// Append records e, dropping the oldest entries past the cap. The
// caller decides what counts as a transition (see
// Entry.SameTransition); Append itself never deduplicates.
func (l *Log) Append(e Entry) {
	l.entries = append(l.entries, e)
	if len(l.entries) > MaxEntries {
		l.entries = append([]Entry(nil), l.entries[len(l.entries)-MaxEntries:]...)
	}
}

// Entries returns a copy of the log, oldest first. The copy means
// callers can slice or sort without touching the stored history.
func (l *Log) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Last returns the most recent entry, or false when the log is
// empty. The reconciler compares against it to decide whether the
// next reconcile is a new transition worth recording.
func (l *Log) Last() (Entry, bool) {
	if len(l.entries) == 0 {
		return Entry{}, false
	}
	return l.entries[len(l.entries)-1], true
}

// Len returns how many entries the log holds.
func (l *Log) Len() int {
	return len(l.entries)
}

// Restore installs entries loaded from the state file, oldest
// first. Entries beyond the cap are dropped from the front, so a
// hand-edited or older state file can never smuggle in an
// unbounded log. Restore replaces whatever the log held.
func (l *Log) Restore(entries []Entry) {
	l.entries = nil
	for _, e := range entries {
		l.Append(e)
	}
}
