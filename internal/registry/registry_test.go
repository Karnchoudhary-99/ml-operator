package registry

import (
	"sync"
	"testing"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func dep(name, ns string, gen int64) api.ModelDeployment {
	return api.ModelDeployment{
		Name:       name,
		Namespace:  ns,
		Generation: gen,
		Spec: api.ModelDeploymentSpec{
			Model: api.ModelSpec{Image: "img:" + name, Version: "v1"},
			Port:  8080,
		},
	}
}

func TestRegisterGetListDelete(t *testing.T) {
	r := New()
	if r.Len() != 0 || r.Revision() != 0 {
		t.Fatalf("new registry not empty: len=%d rev=%d", r.Len(), r.Revision())
	}

	e, existed := r.Register(dep("b-model", "prod", 1))
	if existed || e.Revision != 1 {
		t.Fatalf("first register: existed=%v rev=%d", existed, e.Revision)
	}
	r.Register(dep("a-model", "default", 1))
	r.Register(dep("c-model", "default", 1))
	if r.Len() != 3 {
		t.Fatalf("len = %d, want 3", r.Len())
	}

	got := r.Get("prod/b-model")
	if got == nil || got.Deployment.Name != "b-model" {
		t.Fatalf("Get returned %+v", got)
	}
	if r.Get("default/nope") != nil {
		t.Fatal("Get of missing key should return nil")
	}

	// List is sorted by key.
	list := r.List()
	want := []string{"default/a-model", "default/c-model", "prod/b-model"}
	for i, k := range want {
		if list[i].Deployment.Key() != k {
			t.Fatalf("list[%d] = %s, want %s", i, list[i].Deployment.Key(), k)
		}
	}

	// Delete returns the removed entry; a second delete is nil.
	removed := r.Delete("default/a-model")
	if removed == nil || removed.Deployment.Name != "a-model" {
		t.Fatalf("Delete returned %+v", removed)
	}
	if r.Delete("default/a-model") != nil {
		t.Fatal("second Delete should return nil")
	}
	if r.Len() != 2 {
		t.Fatalf("len after delete = %d, want 2", r.Len())
	}
}

func TestRegisterIdempotent(t *testing.T) {
	r := New()
	d := dep("iris", "default", 1)
	r.Register(d)
	rev := r.Revision()

	// Identical re-registration: no revision bump, no event.
	e, existed := r.Register(d)
	if !existed {
		t.Fatal("re-register should report existed=true")
	}
	if e.Revision != 1 || r.Revision() != rev {
		t.Fatalf("no-op re-register bumped revisions: entry=%d global=%d (was %d)", e.Revision, r.Revision(), rev)
	}

	// New generation: Modified, revisions bump.
	d2 := dep("iris", "default", 2)
	e, _ = r.Register(d2)
	if e.Revision != 2 || r.Revision() != rev+1 {
		t.Fatalf("modified register: entry rev=%d global=%d, want 2/%d", e.Revision, r.Revision(), rev+1)
	}
	if e.Deployment.Generation != 2 {
		t.Fatalf("generation not updated: %d", e.Deployment.Generation)
	}
}

func TestWatchEvents(t *testing.T) {
	r := New()
	ch, unsubscribe := r.Watch()
	defer unsubscribe()

	r.Register(dep("iris", "default", 1))
	d2 := dep("iris", "default", 2)
	r.Register(d2)
	r.Delete("default/iris")
	// No-op re-register of a deleted key is an Add again.
	r.Register(dep("iris", "default", 1))

	want := []EventType{EventAdded, EventModified, EventDeleted, EventAdded}
	for i, typ := range want {
		select {
		case ev := <-ch:
			if ev.Type != typ {
				t.Fatalf("event %d: type %q, want %q", i, ev.Type, typ)
			}
			if ev.Deployment.Name != "iris" {
				t.Fatalf("event %d: wrong deployment %q", i, ev.Deployment.Name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d (%s) never arrived", i, typ)
		}
	}

	// After unsubscribe, no more events reach the channel.
	unsubscribe()
	r.Register(dep("other", "default", 1))
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event after unsubscribe: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// expected: channel stays quiet
	}
}

func TestWatchNoEventOnNoOp(t *testing.T) {
	r := New()
	d := dep("iris", "default", 1)
	r.Register(d)
	ch, unsubscribe := r.Watch()
	defer unsubscribe()
	r.Register(d) // identical: must not notify
	select {
	case ev := <-ch:
		t.Fatalf("no-op re-register produced event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestConcurrentRegister(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := dep("model", "default", int64(i+1))
			r.Register(d)
		}(i)
	}
	wg.Wait()
	if r.Len() != 1 {
		t.Fatalf("len = %d, want 1", r.Len())
	}
	// Run with -race in CI; this just pins the final state is sane.
	if got := r.Get("default/model"); got == nil {
		t.Fatal("model missing after concurrent registers")
	}
}

func TestUpdateStatus(t *testing.T) {
	r := New()
	d := dep("model", "default", 1)
	r.Register(d)
	revBefore := r.Revision()

	ch, unsub := r.Watch()
	defer unsub()

	status := api.ModelDeploymentStatus{
		Phase:              api.PhaseAvailable,
		ReadyReplicas:      2,
		ObservedGeneration: 1,
		Message:            "2/2 replicas ready",
	}
	if !r.UpdateStatus("default/model", status) {
		t.Fatal("UpdateStatus returned false for a registered key")
	}
	if r.Revision() != revBefore+1 {
		t.Fatalf("revision = %d, want %d", r.Revision(), revBefore+1)
	}
	got := r.Get("default/model")
	if got == nil {
		t.Fatal("model missing after UpdateStatus")
	}
	if got.Deployment.Status.Phase != api.PhaseAvailable || got.Deployment.Status.ReadyReplicas != 2 {
		t.Fatalf("status not stored: %+v", got.Deployment.Status)
	}
	// Spec and generation must be untouched by a status write.
	if got.Deployment.Spec.Model.Image != "img:model" || got.Deployment.Generation != 1 {
		t.Fatalf("status write touched spec/generation: %+v", got.Deployment)
	}
	// The entry revision is unchanged: only the global revision moves.
	if got.Revision != 1 {
		t.Fatalf("entry revision = %d, want 1", got.Revision)
	}

	select {
	case ev := <-ch:
		if ev.Type != EventModified || ev.Deployment.Status.Phase != api.PhaseAvailable {
			t.Fatalf("unexpected status event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no Modified event for UpdateStatus")
	}

	// Unknown key: false, no revision bump, no event.
	if r.UpdateStatus("default/nope", status) {
		t.Fatal("UpdateStatus returned true for an unknown key")
	}
	if r.Revision() != revBefore+1 {
		t.Fatalf("revision moved on unknown-key UpdateStatus: %d", r.Revision())
	}
}

// TestSpecsEqualGates compares specs with gate thresholds: two
// separately-built specs with identical gates are equal (the old
// struct comparison compared Gate pointers, so a spec that survived
// a JSON round-trip never equalled a freshly parsed manifest and
// `mlop diff -f` bumped the generation on every identical
// re-apply). A different threshold, or a nil vs set gate, differs.
func TestSpecsEqualGates(t *testing.T) {
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Strategy: api.StrategyCanary,
			Canary: &api.CanarySpec{Weight: 10, Steps: 0, IntervalSeconds: 1,
				Gate: &api.GateSpec{MaxErrorRate: 0.02, MaxP95LatencyMs: 250, MinReadyRatio: 1.0}},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical canary specs with separately-built gates should be equal")
	}
	b.Canary.Gate.MaxErrorRate = 0.05
	if SpecsEqual(a, b) {
		t.Fatal("different gate thresholds should not be equal")
	}
	b = base()
	b.Canary.Gate = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil gate vs set gate should not be equal")
	}
	b = base()
	b.Canary.Weight = 20
	if SpecsEqual(a, b) {
		t.Fatal("different canary weight should not be equal")
	}

	// Blue-green gates compare the same way — and a missing
	// blueGreen block differs from a present one.
	bg := func(gate *api.GateSpec) api.ModelDeploymentSpec {
		s := api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:      8080,
			Strategy:  api.StrategyBlueGreen,
			BlueGreen: &api.BlueGreenSpec{Gate: gate},
		}
		return s
	}
	x, y := bg(&api.GateSpec{MaxErrorRate: 0.02}), bg(&api.GateSpec{MaxErrorRate: 0.02})
	if !SpecsEqual(x, y) {
		t.Fatal("identical blue-green specs with separately-built gates should be equal")
	}
	y.BlueGreen.Gate.MaxErrorRate = 0.05
	if SpecsEqual(x, y) {
		t.Fatal("different blue-green gate thresholds should not be equal")
	}
	y = bg(nil)
	if SpecsEqual(x, y) {
		t.Fatal("nil blue-green gate vs set gate should not be equal")
	}
	z := bg(&api.GateSpec{MaxErrorRate: 0.02})
	z.BlueGreen = nil
	if SpecsEqual(x, z) {
		t.Fatal("present blueGreen block vs absent block should not be equal")
	}
}
