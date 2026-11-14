// Blue-green rollout state machine: the green Deployment soaks at
// full size beside the stable (blue) one while the health gate
// judges it, then the Service flips to green and the green image is
// folded into the stable Deployment. Unlike a canary there is no
// weight ladder — traffic never splits, so the machine has exactly
// two moves: switch (cut the Service over) and fold (make green the
// new stable). A failed gate burns failure budget like a canary's;
// three strikes abort the rollout and the green Deployment is
// deleted, blue keeps serving.
package rollout

// BlueGreenDecision is what the blue-green state machine wants the
// controller to do after one gate evaluation.
type BlueGreenDecision int

const (
	// BlueGreenHold keeps soaking: the gate failed, is still
	// pending, or the green Deployment is not ready yet.
	BlueGreenHold BlueGreenDecision = iota
	// BlueGreenSwitch flips the Service selector from blue to
	// green: the gate passed, so clients move to the new version.
	BlueGreenSwitch
	// BlueGreenFold ends the rollout: the Service already points
	// at green, so the stable Deployment takes the new image, the
	// Service flips back to blue (which is now the new version),
	// and the green Deployment is deleted.
	BlueGreenFold
	// BlueGreenAbort ends the rollout in failure: the gate failed
	// too many times, so the green Deployment is deleted and the
	// deployment is reported Degraded. Blue keeps serving.
	BlueGreenAbort
)

// String returns the decision name for status messages and logs.
func (d BlueGreenDecision) String() string {
	switch d {
	case BlueGreenSwitch:
		return "switch"
	case BlueGreenFold:
		return "fold"
	case BlueGreenAbort:
		return "abort"
	default:
		return "hold"
	}
}

// BlueGreenState is the per-deployment blue-green progress the
// controller keeps between reconciles. It is a value: BlueGreenNext
// returns the new state, the caller stores it.
type BlueGreenState struct {
	// Generation is the spec generation this rollout belongs to. A
	// new generation (a new model version) resets the rollout.
	Generation int64
	// Failures is the consecutive gate failures while soaking.
	Failures int
	// Switched is true once the Service has been cut over to
	// green. The next reconcile folds green into stable.
	Switched bool
	// Done is true once the rollout folded or aborted; a done
	// state always decides Hold.
	Done bool
	// Aborted is true when Done came from an abort rather than a
	// fold.
	Aborted bool
}

// NewBlueGreen starts a blue-green rollout for generation: green
// soaks, nothing has switched yet.
func NewBlueGreen(generation int64) BlueGreenState {
	return BlueGreenState{Generation: generation}
}

// BlueGreenNext applies one gate evaluation to the state and returns
// the new state plus the decision. The rules, in order:
//   - a done state always holds (terminal);
//   - a switched state always folds (the cutover already happened;
//     the soak gate was the safety check, the switch is the commit);
//   - a pending gate holds without counting a failure;
//   - a failed gate counts a failure and aborts at maxFailures;
//   - a passed gate switches the Service to green.
func BlueGreenNext(s BlueGreenState, ev Evaluation, maxFailures int) (BlueGreenState, BlueGreenDecision) {
	if s.Done {
		return s, BlueGreenHold
	}
	if s.Switched {
		s.Done = true
		return s, BlueGreenFold
	}
	if maxFailures <= 0 {
		maxFailures = DefaultMaxFailures
	}
	if ev.Pending {
		return s, BlueGreenHold
	}
	if !ev.Pass {
		s.Failures++
		if s.Failures >= maxFailures {
			s.Done = true
			s.Aborted = true
			return s, BlueGreenAbort
		}
		return s, BlueGreenHold
	}
	s.Failures = 0
	s.Switched = true
	return s, BlueGreenSwitch
}
