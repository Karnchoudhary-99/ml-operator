package rollout

import (
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func pass() Evaluation    { return Evaluation{Pass: true} }
func fail() Evaluation    { return Evaluation{Pass: false, Reasons: []string{"gate failed"}} }
func pending() Evaluation { return Evaluation{Pending: true, Reasons: []string{"starting"}} }

func TestStateMachine(t *testing.T) {
	spec := api.CanarySpec{Weight: 10, Steps: 2, IntervalSeconds: 30}
	now := time.Now()
	interval := 30 * time.Second

	t.Run("starts at the first ladder weight", func(t *testing.T) {
		s := New(1, spec, now)
		if got := s.CurrentWeight(); got != 10 {
			t.Fatalf("CurrentWeight = %d, want 10", got)
		}
		if s.AtFinalStep() {
			t.Fatal("must not start at the final step")
		}
	})

	t.Run("holds before the interval elapses", func(t *testing.T) {
		s := New(1, spec, now)
		next, d := Next(s, pass(), now.Add(10*time.Second), interval, 0)
		if d != DecisionHold || next.Step != 0 {
			t.Fatalf("decision = %v step = %d, want hold step 0", d, next.Step)
		}
	})

	t.Run("advances once the interval elapses", func(t *testing.T) {
		s := New(1, spec, now)
		next, d := Next(s, pass(), now.Add(30*time.Second), interval, 0)
		if d != DecisionAdvance || next.Step != 1 || next.CurrentWeight() != 55 {
			t.Fatalf("decision = %v step = %d weight = %d, want advance step 1 weight 55", d, next.Step, next.CurrentWeight())
		}
	})

	t.Run("promotes at the final step", func(t *testing.T) {
		s := New(1, spec, now)
		s.Step = 2 // ladder [10 55 100]
		next, d := Next(s, pass(), now.Add(time.Hour), interval, 0)
		if d != DecisionPromote || !next.Done || next.Aborted {
			t.Fatalf("decision = %v done = %v aborted = %v, want promote done not-aborted", d, next.Done, next.Aborted)
		}
	})

	t.Run("a done state always holds", func(t *testing.T) {
		s := New(1, spec, now)
		s.Done = true
		_, d := Next(s, pass(), now.Add(time.Hour), interval, 0)
		if d != DecisionHold {
			t.Fatalf("decision = %v, want hold", d)
		}
	})

	t.Run("aborts after three consecutive failures", func(t *testing.T) {
		s := New(1, spec, now)
		var d Decision
		for i := 0; i < 2; i++ {
			s, d = Next(s, fail(), now, interval, 0)
			if d != DecisionHold {
				t.Fatalf("failure %d: decision = %v, want hold", i+1, d)
			}
		}
		s, d = Next(s, fail(), now, interval, 0)
		if d != DecisionAbort || !s.Done || !s.Aborted {
			t.Fatalf("decision = %v done = %v aborted = %v, want abort done aborted", d, s.Done, s.Aborted)
		}
	})

	t.Run("a pass clears the failure count", func(t *testing.T) {
		s := New(1, spec, now)
		s, _ = Next(s, fail(), now, interval, 0)
		s, _ = Next(s, fail(), now, interval, 0)
		if s.Failures != 2 {
			t.Fatalf("Failures = %d, want 2", s.Failures)
		}
		s, d := Next(s, pass(), now.Add(30*time.Second), interval, 0)
		if d != DecisionAdvance || s.Failures != 0 {
			t.Fatalf("decision = %v failures = %d, want advance failures 0", d, s.Failures)
		}
		// Two fresh failures after the reset must not abort.
		s, _ = Next(s, fail(), now, interval, 0)
		s, d = Next(s, fail(), now, interval, 0)
		if d != DecisionHold {
			t.Fatalf("decision = %v, want hold (only 2 failures after reset)", d)
		}
	})

	t.Run("pending never counts as a failure", func(t *testing.T) {
		s := New(1, spec, now)
		for i := 0; i < 5; i++ {
			var d Decision
			s, d = Next(s, pending(), now, interval, 0)
			if d != DecisionHold {
				t.Fatalf("pending %d: decision = %v, want hold", i+1, d)
			}
		}
		if s.Failures != 0 {
			t.Fatalf("Failures = %d, want 0 after only pending evaluations", s.Failures)
		}
	})

	t.Run("custom max failures respected", func(t *testing.T) {
		s := New(1, spec, now)
		s, d := Next(s, fail(), now, interval, 1)
		if d != DecisionAbort {
			t.Fatalf("decision = %v, want abort with maxFailures 1", d)
		}
	})
}
