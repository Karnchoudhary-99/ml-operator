package rollout

import (
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// Decision is what the rollout state machine wants the controller to
// do after one gate evaluation.
type Decision int

const (
	// DecisionHold keeps the current weight: the gate failed, is
	// still pending, or the step interval has not elapsed yet.
	DecisionHold Decision = iota
	// DecisionAdvance moves the canary to the next weight on the
	// ladder; the controller re-renders it on the next reconcile.
	DecisionAdvance
	// DecisionPromote ends the rollout: the gate passed at the final
	// weight, so the stable Deployment takes the new image and the
	// canary Deployment is deleted.
	DecisionPromote
	// DecisionAbort ends the rollout in failure: the gate failed too
	// many times in a row, so the canary Deployment is deleted and
	// the deployment is reported Degraded. The stable Deployment
	// keeps serving the previous version.
	DecisionAbort
)

// String returns the decision name for status messages and logs.
func (d Decision) String() string {
	switch d {
	case DecisionAdvance:
		return "advance"
	case DecisionPromote:
		return "promote"
	case DecisionAbort:
		return "abort"
	default:
		return "hold"
	}
}

// DefaultMaxFailures is the consecutive gate failures that abort a
// rollout. Three strikes: a single bad sample (a slow pod, one
// error burst) holds the rollout, it does not kill it.
const DefaultMaxFailures = 3

// State is the per-deployment rollout progress the controller keeps
// between reconciles. It is a value: Next returns the new state, the
// caller stores it.
type State struct {
	// Generation is the spec generation this rollout belongs to. A
	// new generation (a new model version) resets the rollout.
	Generation int64
	// Weights is the ladder from StepWeights.
	Weights []int32
	// Step is the index into Weights of the current weight.
	Step int
	// Failures is the consecutive gate failures at the current step.
	Failures int
	// LastAdvance is when the current step started; the next step
	// waits until the spec interval has elapsed since then.
	LastAdvance time.Time
	// Done is true once the rollout promoted or aborted; a done
	// state always decides Hold.
	Done bool
	// Aborted is true when Done came from an abort rather than a
	// promotion.
	Aborted bool
}

// New starts a rollout for generation at the first ladder weight.
// now is when the rollout started, so the first step also waits out
// the interval before advancing.
func New(generation int64, spec api.CanarySpec, now time.Time) State {
	return State{
		Generation:  generation,
		Weights:     StepWeights(spec),
		Step:        0,
		LastAdvance: now,
	}
}

// CurrentWeight returns the traffic weight of the current step.
func (s State) CurrentWeight() int32 {
	if len(s.Weights) == 0 || s.Step < 0 || s.Step >= len(s.Weights) {
		return 0
	}
	return s.Weights[s.Step]
}

// AtFinalStep reports whether the rollout is on the last ladder
// weight — a passing gate here promotes instead of advancing.
func (s State) AtFinalStep() bool {
	return len(s.Weights) > 0 && s.Step >= len(s.Weights)-1
}

// Next applies one gate evaluation to the state and returns the new
// state plus the decision. The rules, in order:
//   - a done state always holds (terminal);
//   - a pending gate holds without counting a failure;
//   - a failed gate counts a failure and aborts at maxFailures;
//   - a passed gate clears the failure count, then promotes at the
//     final step, advances once the interval has elapsed, or holds.
func Next(s State, ev Evaluation, now time.Time, interval time.Duration, maxFailures int) (State, Decision) {
	if s.Done {
		return s, DecisionHold
	}
	if maxFailures <= 0 {
		maxFailures = DefaultMaxFailures
	}
	if ev.Pending {
		return s, DecisionHold
	}
	if !ev.Pass {
		s.Failures++
		if s.Failures >= maxFailures {
			s.Done = true
			s.Aborted = true
			return s, DecisionAbort
		}
		return s, DecisionHold
	}
	s.Failures = 0
	if s.AtFinalStep() {
		s.Done = true
		return s, DecisionPromote
	}
	if now.Sub(s.LastAdvance) < interval {
		return s, DecisionHold
	}
	s.Step++
	s.LastAdvance = now
	return s, DecisionAdvance
}
