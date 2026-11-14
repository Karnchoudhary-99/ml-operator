package rollout

import "testing"

func TestBlueGreenNext(t *testing.T) {
	pass := Evaluation{Pass: true}
	fail := Evaluation{Pass: false, Reasons: []string{"error rate too high"}}
	pending := Evaluation{Pending: true, Reasons: []string{"pods still starting"}}

	// A passing gate switches the Service to green.
	s, d := BlueGreenNext(NewBlueGreen(3), pass, 0)
	if d != BlueGreenSwitch {
		t.Fatalf("pass: decision = %v, want switch", d)
	}
	if !s.Switched || s.Done {
		t.Fatalf("pass: state = %+v, want Switched and not Done", s)
	}

	// The next reconcile folds green into stable.
	s, d = BlueGreenNext(s, pass, 0)
	if d != BlueGreenFold {
		t.Fatalf("switched: decision = %v, want fold", d)
	}
	if !s.Done || s.Aborted {
		t.Fatalf("fold: state = %+v, want Done and not Aborted", s)
	}

	// A done state always holds, whatever the gate says.
	if _, d := BlueGreenNext(s, pass, 0); d != BlueGreenHold {
		t.Fatalf("done: decision = %v, want hold", d)
	}

	// A pending gate holds and burns no failure budget.
	s = NewBlueGreen(3)
	s, d = BlueGreenNext(s, pending, 0)
	if d != BlueGreenHold || s.Failures != 0 {
		t.Fatalf("pending: decision = %v, failures = %d, want hold/0", d, s.Failures)
	}

	// Failures accumulate; the third strike aborts.
	s = NewBlueGreen(3)
	for i := 1; i <= 2; i++ {
		s, d = BlueGreenNext(s, fail, 0)
		if d != BlueGreenHold {
			t.Fatalf("failure %d: decision = %v, want hold", i, d)
		}
		if s.Failures != i {
			t.Fatalf("failure %d: Failures = %d", i, s.Failures)
		}
	}
	s, d = BlueGreenNext(s, fail, 0)
	if d != BlueGreenAbort {
		t.Fatalf("third failure: decision = %v, want abort", d)
	}
	if !s.Done || !s.Aborted {
		t.Fatalf("abort: state = %+v, want Done and Aborted", s)
	}

	// A pass clears the failure count before switching.
	s = NewBlueGreen(3)
	s, _ = BlueGreenNext(s, fail, 0)
	s, d = BlueGreenNext(s, pass, 0)
	if d != BlueGreenSwitch || s.Failures != 0 {
		t.Fatalf("pass after failure: decision = %v, failures = %d, want switch/0", d, s.Failures)
	}

	// A switched state folds even if the gate now fails: the
	// cutover already happened, the soak gate was the safety check.
	s = NewBlueGreen(3)
	s, _ = BlueGreenNext(s, pass, 0)
	if _, d := BlueGreenNext(s, fail, 0); d != BlueGreenFold {
		t.Fatalf("switched + failing gate: decision = %v, want fold", d)
	}
}
