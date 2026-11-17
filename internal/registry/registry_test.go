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

// TestSpecsEqualAvailability compares specs with availability
// blocks: two separately-built specs with identical blocks are
// equal (the anti-affinity flag is a pointer, so a plain struct
// comparison would compare addresses and a spec that survived a
// JSON round-trip would never equal a freshly parsed manifest).
// A different minAvailable, maxSkew, or flag, or a nil vs set
// block, differs — an availability-only change must bump the
// generation, or `mlop apply` would call it unchanged and the PDB
// would never update.
func TestSpecsEqualAvailability(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Replicas: func() *int32 { v := int32(3); return &v }(),
			Strategy: api.StrategyRolling,
			Availability: &api.AvailabilitySpec{
				MinAvailable: 2, AntiAffinity: boolPtr(true), MaxSkew: 1,
			},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical availability specs should be equal")
	}
	b.Availability.MinAvailable = 1
	if SpecsEqual(a, b) {
		t.Fatal("different minAvailable should not be equal")
	}
	b = base()
	b.Availability.MaxSkew = 2
	if SpecsEqual(a, b) {
		t.Fatal("different maxSkew should not be equal")
	}
	b = base()
	b.Availability.AntiAffinity = boolPtr(false)
	if SpecsEqual(a, b) {
		t.Fatal("different antiAffinity flag should not be equal")
	}
	b = base()
	b.Availability.AntiAffinity = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil antiAffinity flag vs set flag should not be equal")
	}
	b = base()
	b.Availability = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil availability block vs set block should not be equal")
	}
}

// TestSpecsEqualSecurity: a security-only change is a spec
// change — it must bump the generation on apply, or the
// reconciler would never render the new security context. The
// availability bug this guards against (session 4): SpecsEqual
// ignored Availability, so an availability-only change was
// treated as unchanged and the PDB never updated.
func TestSpecsEqualSecurity(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	int64Ptr := func(v int64) *int64 { return &v }
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Replicas: func() *int32 { v := int32(3); return &v }(),
			Strategy: api.StrategyRolling,
			Security: &api.SecuritySpec{
				RunAsNonRoot:             boolPtr(true),
				RunAsUser:                int64Ptr(10001),
				ReadOnlyRootFilesystem:   boolPtr(true),
				AllowPrivilegeEscalation: boolPtr(false),
				DropCapabilities:         []string{"ALL"},
				SeccompProfile:           "RuntimeDefault",
			},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical security specs should be equal")
	}
	// Same values through fresh pointers must still be equal:
	// the state file round-trips through JSON, so pointer
	// identity can never be the comparison.
	b = base()
	if !SpecsEqual(a, b) {
		t.Fatal("security specs with equal values but fresh pointers should be equal")
	}
	for _, mutate := range []struct {
		name string
		f    func(*api.SecuritySpec)
	}{
		{"runAsNonRoot", func(s *api.SecuritySpec) { s.RunAsNonRoot = boolPtr(false) }},
		{"runAsUser", func(s *api.SecuritySpec) { s.RunAsUser = int64Ptr(20000) }},
		{"readOnlyRootFilesystem", func(s *api.SecuritySpec) { s.ReadOnlyRootFilesystem = boolPtr(false) }},
		{"allowPrivilegeEscalation", func(s *api.SecuritySpec) { s.AllowPrivilegeEscalation = boolPtr(true) }},
		{"dropCapabilities", func(s *api.SecuritySpec) { s.DropCapabilities = []string{"NET_BIND_SERVICE"} }},
		{"seccompProfile", func(s *api.SecuritySpec) { s.SeccompProfile = "Unconfined" }},
	} {
		b = base()
		mutate.f(b.Security)
		if SpecsEqual(a, b) {
			t.Fatalf("different %s should not be equal", mutate.name)
		}
	}
	b = base()
	b.Security.RunAsUser = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil runAsUser vs set uid should not be equal")
	}
	b = base()
	b.Security = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil security block vs set block should not be equal")
	}
}

// TestSpecsEqualPlacement: a placement-only change is a spec
// change — it must bump the generation on apply, or the
// reconciler would never render the new nodes. The maintenance
// window bug this guards against: SpecsEqual ignored the window,
// so a re-windowed manifest was treated as unchanged and the
// reconciler kept the old window.
func TestSpecsEqualPlacement(t *testing.T) {
	int64Ptr := func(v int64) *int64 { return &v }
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Replicas: func() *int32 { v := int32(3); return &v }(),
			Strategy: api.StrategyRolling,
			Placement: &api.PlacementSpec{
				NodeSelector: map[string]string{"accelerator": "nvidia-tesla-t4"},
				Tolerations: []api.Toleration{
					{Key: "gpu", Value: "true", Effect: "NoSchedule"},
					{Key: "dedicated", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: int64Ptr(300)},
				},
			},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical placement specs should be equal")
	}
	// Same values through fresh pointers must still be equal:
	// the state file round-trips through JSON, so pointer
	// identity can never be the comparison.
	b = base()
	if !SpecsEqual(a, b) {
		t.Fatal("placement specs with equal values but fresh pointers should be equal")
	}
	// Map order is meaningless: a re-ordered nodeSelector is
	// the same placement.
	b = base()
	b.Placement.NodeSelector = map[string]string{"accelerator": "nvidia-tesla-t4"}
	if !SpecsEqual(a, b) {
		t.Fatal("re-ordered nodeSelector should still be equal")
	}
	for _, mutate := range []struct {
		name string
		f    func(*api.PlacementSpec)
	}{
		{"nodeSelector value", func(p *api.PlacementSpec) { p.NodeSelector["accelerator"] = "nvidia-tesla-v100" }},
		{"nodeSelector key added", func(p *api.PlacementSpec) { p.NodeSelector["zone"] = "a" }},
		{"toleration value", func(p *api.PlacementSpec) { p.Tolerations[0].Value = "false" }},
		{"toleration effect", func(p *api.PlacementSpec) { p.Tolerations[0].Effect = "PreferNoSchedule" }},
		{"toleration operator", func(p *api.PlacementSpec) { p.Tolerations[0].Operator = "Exists" }},
		{"toleration seconds", func(p *api.PlacementSpec) { p.Tolerations[1].TolerationSeconds = int64Ptr(60) }},
		{"toleration removed", func(p *api.PlacementSpec) { p.Tolerations = p.Tolerations[:1] }},
	} {
		b = base()
		mutate.f(b.Placement)
		if SpecsEqual(a, b) {
			t.Fatalf("different %s should not be equal", mutate.name)
		}
	}
	b = base()
	b.Placement.Tolerations[1].TolerationSeconds = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil tolerationSeconds vs set seconds should not be equal")
	}
	b = base()
	b.Placement = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil placement block vs set block should not be equal")
	}
}

// TestSpecsEqualPaused: toggling Paused is a spec change — it must
// bump the generation on apply, or a paused manifest would be
// reported unchanged and the reconciler would never see the flag.
func TestSpecsEqualPaused(t *testing.T) {
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Strategy: api.StrategyRolling,
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical unpaused specs should be equal")
	}
	b.Paused = true
	if SpecsEqual(a, b) {
		t.Fatal("paused vs unpaused specs should not be equal")
	}
	a.Paused, b.Paused = true, true
	if !SpecsEqual(a, b) {
		t.Fatal("identical paused specs should be equal")
	}
}

// TestSpecsEqualMaintenanceWindow pins the window comparison: a
// changed window must bump the generation (this bit the feature
// itself — a re-windowed manifest applied as "unchanged" and the
// reconciler kept deferring under the old window), while
// re-ordering the days or changing their case must not.
func TestSpecsEqualMaintenanceWindow(t *testing.T) {
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Strategy: api.StrategyRolling,
			MaintenanceWindow: &api.MaintenanceWindowSpec{
				Days:  []string{"Mon", "Wed", "Fri"},
				Start: "02:00",
				End:   "04:00",
			},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical windowed specs should be equal")
	}
	b.MaintenanceWindow = &api.MaintenanceWindowSpec{Days: []string{"Mon", "Wed", "Fri"}, Start: "02:00", End: "05:00"}
	if SpecsEqual(a, b) {
		t.Fatal("different end time should not be equal")
	}
	b.MaintenanceWindow = &api.MaintenanceWindowSpec{Days: []string{"Mon", "Tue"}, Start: "02:00", End: "04:00"}
	if SpecsEqual(a, b) {
		t.Fatal("different days should not be equal")
	}
	b.MaintenanceWindow = nil
	if SpecsEqual(a, b) {
		t.Fatal("windowed vs windowless specs should not be equal")
	}
	b = base()
	b.MaintenanceWindow.Days = []string{"fri", "Mon", "WED"}
	if !SpecsEqual(a, b) {
		t.Fatal("re-ordered / re-cased days are the same window and should be equal")
	}
}

// TestRegisterPreservesStatus: re-registering a deployment with a
// new spec (the apply path) must not wipe the stored status — a
// parsed manifest carries a zero status, and overwriting would lose
// the phase, the conditions, and the rollback target. This bit the
// pause feature: pausing a live deployment reported Pending and
// lost PreviousImage until the fix.
func TestRegisterPreservesStatus(t *testing.T) {
	r := New()
	d := api.ModelDeployment{
		Name: "x", Namespace: "default", Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model: api.ModelSpec{Image: "img:v1", Version: "v1"},
			Port:  8080,
		},
	}
	r.Register(d)
	st := api.ModelDeploymentStatus{
		Phase: api.PhaseAvailable, ReadyReplicas: 2,
		PreviousImage: "img:v0", PreviousVersion: "v0",
	}
	if !r.UpdateStatus("default/x", st) {
		t.Fatal("UpdateStatus failed")
	}

	// A new spec with a zero status: the stored status survives.
	d2 := d
	d2.Generation = 2
	d2.Spec.Model.Image = "img:v2"
	d2.Spec.Model.Version = "v2"
	r.Register(d2)
	got := r.Get("default/x").Deployment
	if got.Status.Phase != api.PhaseAvailable || got.Status.PreviousImage != "img:v0" {
		t.Fatalf("re-register wiped status: %+v", got.Status)
	}
	if got.Spec.Model.Image != "img:v2" || got.Generation != 2 {
		t.Fatalf("re-register did not take the new spec: %+v", got)
	}

	// Inserting a fresh key keeps the status as given (Restore
	// re-registers deployments from the state file this way).
	d3 := api.ModelDeployment{
		Name: "y", Namespace: "default", Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model: api.ModelSpec{Image: "img:v1", Version: "v1"},
			Port:  8080,
		},
		Status: st,
	}
	r.Register(d3)
	if got := r.Get("default/y").Deployment.Status; got.Phase != api.PhaseAvailable {
		t.Fatalf("fresh register dropped status: %+v", got)
	}
}

// TestSpecsEqualModelSource: a model-source-only change is a
// spec change — it must bump the generation on apply, or the
// reconciler would keep serving the old artifact. Same class
// as the placement bug: a field SpecsEqual ignores is a field
// the operator silently never updates.
func TestSpecsEqualModelSource(t *testing.T) {
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:    api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:     8080,
			Replicas: func() *int32 { v := int32(3); return &v }(),
			Strategy: api.StrategyRolling,
			ModelSource: &api.ModelSourceSpec{
				URI:    "https://artifacts.example.com/models/iris-mlp.onnx",
				SHA256: "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807",
			},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical model sources should be equal")
	}
	// Same values through fresh pointers must still be equal.
	b = base()
	if !SpecsEqual(a, b) {
		t.Fatal("model sources with equal values but fresh pointers should be equal")
	}
	for _, mutate := range []struct {
		name string
		f    func(*api.ModelSourceSpec)
	}{
		{"URI", func(m *api.ModelSourceSpec) { m.URI = "https://artifacts.example.com/models/iris-mlp-v2.onnx" }},
		{"SHA256", func(m *api.ModelSourceSpec) {
			m.SHA256 = "00004a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807"
		}},
		{"SHA256 cleared", func(m *api.ModelSourceSpec) { m.SHA256 = "" }},
		{"MountPath", func(m *api.ModelSourceSpec) { m.MountPath = "/data/models" }},
		{"DownloaderImage", func(m *api.ModelSourceSpec) { m.DownloaderImage = "curlimages/curl:8.9.0" }},
	} {
		b = base()
		mutate.f(b.ModelSource)
		if SpecsEqual(a, b) {
			t.Fatalf("different %s should not be equal", mutate.name)
		}
	}
	b = base()
	b.ModelSource = nil
	if SpecsEqual(a, b) {
		t.Fatal("nil modelSource vs set block should not be equal")
	}
}

// TestSpecsEqualImagePullSecrets: a pull-secret-only change
// is a spec change — it must bump the generation on apply, or
// the reconciler would keep pulling with the old credentials.
// Same class as the placement bug: a field SpecsEqual ignores
// is a field the operator silently never updates.
func TestSpecsEqualImagePullSecrets(t *testing.T) {
	base := func() api.ModelDeploymentSpec {
		return api.ModelDeploymentSpec{
			Model:            api.ModelSpec{Image: "img:v2", Version: "v2"},
			Port:             8080,
			Replicas:         func() *int32 { v := int32(3); return &v }(),
			Strategy:         api.StrategyRolling,
			ImagePullSecrets: []string{"regcred", "ghcr-creds"},
		}
	}
	a, b := base(), base()
	if !SpecsEqual(a, b) {
		t.Fatal("identical pull secrets should be equal")
	}
	// Same values through fresh slices must still be equal.
	b = base()
	if !SpecsEqual(a, b) {
		t.Fatal("pull secrets with equal values but fresh slices should be equal")
	}
	for _, mutate := range []struct {
		name string
		f    func(*api.ModelDeploymentSpec)
	}{
		{"secret added", func(s *api.ModelDeploymentSpec) { s.ImagePullSecrets = append(s.ImagePullSecrets, "extra") }},
		{"secret removed", func(s *api.ModelDeploymentSpec) { s.ImagePullSecrets = s.ImagePullSecrets[:1] }},
		{"secret rotated", func(s *api.ModelDeploymentSpec) { s.ImagePullSecrets[0] = "regcred-v2" }},
		{"secrets cleared", func(s *api.ModelDeploymentSpec) { s.ImagePullSecrets = nil }},
	} {
		b = base()
		mutate.f(&b)
		if SpecsEqual(a, b) {
			t.Fatalf("different %s should not be equal", mutate.name)
		}
	}
	// An empty list and a nil list are the same: no secrets.
	a.ImagePullSecrets = []string{}
	b = base()
	b.ImagePullSecrets = nil
	if !SpecsEqual(a, b) {
		t.Fatal("empty and nil pull-secret lists should be equal")
	}
}
