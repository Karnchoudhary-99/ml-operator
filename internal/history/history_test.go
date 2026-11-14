package history

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func entry(gen int64, phase api.Phase) Entry {
	return Entry{
		Generation: gen,
		Image:      "iris-mlp",
		Version:    "v1",
		Phase:      phase,
		Reason:     "test",
		At:         time.Unix(1000+gen, 0).UTC(),
	}
}

func TestAppendAndEntriesOldestFirst(t *testing.T) {
	l := New()
	if got := l.Entries(); len(got) != 0 {
		t.Fatalf("new log has %d entries, want 0", len(got))
	}
	l.Append(entry(1, api.PhaseProgressing))
	l.Append(entry(1, api.PhaseAvailable))
	got := l.Entries()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Phase != api.PhaseProgressing || got[1].Phase != api.PhaseAvailable {
		t.Fatalf("entries out of order: %+v", got)
	}
	// Entries returns a copy: mutating it must not touch the log.
	got[0].Phase = api.PhaseFailed
	if l.Entries()[0].Phase != api.PhaseProgressing {
		t.Fatal("Entries() did not return a copy")
	}
}

func TestCapDropsOldest(t *testing.T) {
	l := New()
	for i := int64(0); i < MaxEntries+5; i++ {
		l.Append(entry(i, api.PhaseAvailable))
	}
	got := l.Entries()
	if len(got) != MaxEntries {
		t.Fatalf("got %d entries, want cap %d", len(got), MaxEntries)
	}
	if got[0].Generation != 5 {
		t.Fatalf("oldest kept generation = %d, want 5 (first 5 dropped)", got[0].Generation)
	}
	if got[len(got)-1].Generation != MaxEntries+4 {
		t.Fatalf("newest kept generation = %d, want %d", got[len(got)-1].Generation, MaxEntries+4)
	}
}

func TestLast(t *testing.T) {
	l := New()
	if _, ok := l.Last(); ok {
		t.Fatal("empty log Last() returned an entry")
	}
	l.Append(entry(1, api.PhasePending))
	l.Append(entry(2, api.PhaseAvailable))
	last, ok := l.Last()
	if !ok || last.Generation != 2 || last.Phase != api.PhaseAvailable {
		t.Fatalf("Last() = %+v, %v, want gen 2 Available", last, ok)
	}
}

func TestSameTransition(t *testing.T) {
	e := entry(3, api.PhaseAvailable)
	if !e.SameTransition(3, "iris-mlp", "v1", api.PhaseAvailable) {
		t.Fatal("identical tuple should be the same transition")
	}
	cases := []struct {
		name    string
		gen     int64
		image   string
		version string
		phase   api.Phase
	}{
		{"generation", 4, "iris-mlp", "v1", api.PhaseAvailable},
		{"image", 3, "iris-mlp-v2", "v1", api.PhaseAvailable},
		{"version", 3, "iris-mlp", "v2", api.PhaseAvailable},
		{"phase", 3, "iris-mlp", "v1", api.PhaseDegraded},
	}
	for _, c := range cases {
		if e.SameTransition(c.gen, c.image, c.version, c.phase) {
			t.Fatalf("%s: different tuple reported as the same transition", c.name)
		}
	}
}

func TestRestoreCapsAndReplaces(t *testing.T) {
	l := New()
	l.Append(entry(1, api.PhaseAvailable))
	var entries []Entry
	for i := int64(0); i < MaxEntries+2; i++ {
		entries = append(entries, entry(100+i, api.PhaseProgressing))
	}
	l.Restore(entries)
	got := l.Entries()
	if len(got) != MaxEntries {
		t.Fatalf("restored %d entries, want cap %d", len(got), MaxEntries)
	}
	if got[0].Generation != 102 {
		t.Fatalf("oldest restored generation = %d, want 102", got[0].Generation)
	}
	// Restoring an empty list clears the log.
	l.Restore(nil)
	if l.Len() != 0 {
		t.Fatalf("Restore(nil) left %d entries, want 0", l.Len())
	}
}
