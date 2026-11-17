package controller

import (
	"errors"
	"sync"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// errTransientApplyFailure is the error a failing FakeCluster
// apply returns. It stands in for the transient cluster errors a
// real API server returns under load (timeouts, etcd hiccups,
// admission webhook blips) — the kind the reconciler must ride
// out with a retry instead of degrading the deployment.
var errTransientApplyFailure = errors.New("fake cluster: transient apply failure")

// FakeCluster is an in-memory Cluster for tests and the operator demo
// loop. It stores exactly what was applied — no rollout simulation —
// so a test controls readiness explicitly with SetReady, the way a
// real cluster would report it asynchronously after pods come up.
type FakeCluster struct {
	mu          sync.Mutex
	deployments map[string]render.Deployment
	services    map[string]render.Service
	hpas        map[string]render.HPA
	pdbs        map[string]render.PodDisruptionBudget
	metrics     map[string]ClusterMetrics
	ready       map[string]int32
	// failApplies is the test-only chaos hook: while it is > 0,
	// every mutating call (apply or delete, any object) fails
	// with errTransientApplyFailure and decrements it. Tests use
	// it to prove the reconciler retries transient failures
	// instead of degrading on the first one. Production code
	// never sets it.
	failApplies int
}

// NewFakeCluster returns an empty fake cluster.
func NewFakeCluster() *FakeCluster {
	return &FakeCluster{
		deployments: make(map[string]render.Deployment),
		services:    make(map[string]render.Service),
		hpas:        make(map[string]render.HPA),
		pdbs:        make(map[string]render.PodDisruptionBudget),
		metrics:     make(map[string]ClusterMetrics),
		ready:       make(map[string]int32),
	}
}

// GetDeployment implements Cluster.
func (f *FakeCluster) GetDeployment(key string) (render.Deployment, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deployments[key]
	return d, ok
}

// FailNextApplies arms the chaos hook: the next n mutating calls
// (ApplyDeployment, ApplyService, ApplyHPA, ApplyPDB, and the
// deletes) fail with a transient error, then the cluster behaves
// normally again. It is test-only — the demo and the CLI never
// call it.
func (f *FakeCluster) FailNextApplies(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failApplies = n
}

// consumeFailure reports whether the next mutating call should
// fail: true while the chaos hook still has failures left, and it
// spends one. Callers hold the lock.
func (f *FakeCluster) consumeFailure() bool {
	if f.failApplies <= 0 {
		return false
	}
	f.failApplies--
	return true
}

// ApplyDeployment implements Cluster. A newly applied Deployment
// starts with 0 ready replicas — pods take time to come up — until
// the test (or demo) calls SetReady. Re-applying an existing
// Deployment does NOT reset readiness: the fake does not simulate
// pod restarts, so a test that needs a readiness drop after an
// update must call SetReady explicitly. One exception: a scale-down
// terminates pods, so re-applying with fewer replicas clamps the
// stored ready count to the new replica count — without the clamp
// a Deployment scaled to 0 would still report its old pods ready.
// A scale-up never raises readiness (the new pods are not ready
// yet), so the clamp only ever lowers it.
func (f *FakeCluster) ApplyDeployment(d render.Deployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	key := d.Namespace + "/" + d.Name
	if _, ok := f.deployments[key]; !ok {
		f.ready[key] = 0
	} else if f.ready[key] > d.Replicas {
		f.ready[key] = d.Replicas
	}
	f.deployments[key] = d
	return nil
}

// DeleteDeployment implements Cluster.
func (f *FakeCluster) DeleteDeployment(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	delete(f.deployments, key)
	delete(f.ready, key)
	delete(f.metrics, key)
	return nil
}

// GetService implements Cluster.
func (f *FakeCluster) GetService(key string) (render.Service, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[key]
	return s, ok
}

// ApplyService implements Cluster.
func (f *FakeCluster) ApplyService(s render.Service) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	f.services[s.Namespace+"/"+s.Name] = s
	return nil
}

// DeleteService implements Cluster.
func (f *FakeCluster) DeleteService(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	delete(f.services, key)
	return nil
}

// GetHPA implements Cluster.
func (f *FakeCluster) GetHPA(key string) (render.HPA, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hpas[key]
	return h, ok
}

// ApplyHPA implements Cluster.
func (f *FakeCluster) ApplyHPA(h render.HPA) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	f.hpas[h.Namespace+"/"+h.Name] = h
	return nil
}

// DeleteHPA implements Cluster.
func (f *FakeCluster) DeleteHPA(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	delete(f.hpas, key)
	return nil
}

// GetPDB implements Cluster.
func (f *FakeCluster) GetPDB(key string) (render.PodDisruptionBudget, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pdbs[key]
	return p, ok
}

// ApplyPDB implements Cluster.
func (f *FakeCluster) ApplyPDB(p render.PodDisruptionBudget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	f.pdbs[p.Namespace+"/"+p.Name] = p
	return nil
}

// DeletePDB implements Cluster.
func (f *FakeCluster) DeletePDB(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeFailure() {
		return errTransientApplyFailure
	}
	delete(f.pdbs, key)
	return nil
}

// Metrics implements Cluster.
func (f *FakeCluster) Metrics(key string) (ClusterMetrics, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.metrics[key]
	return m, ok
}

// SetMetrics records a load/health observation for the Deployment
// at key, the way a metrics server would report it. It is a no-op
// for a Deployment the fake has never seen.
func (f *FakeCluster) SetMetrics(key string, m ClusterMetrics) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.deployments[key]; ok {
		f.metrics[key] = m
	}
}

// setModelLoad records one model-load probe outcome for the
// Deployment at key, keeping any load/health observation already
// recorded for it. It is a no-op for a Deployment the fake has
// never seen — a probe only makes sense for applied objects. The
// probe state lives in the metrics entry, so Snapshot/Restore
// persist it with the rest of the cluster state.
func (f *FakeCluster) setModelLoad(key string, known, loaded, failed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.deployments[key]; !ok {
		return
	}
	m, ok := f.metrics[key]
	if !ok {
		// No load observation yet: keep error rate unknown (-1)
		// instead of the zero value, which would read as "0 errors
		// observed".
		m = ClusterMetrics{ErrorRate: -1}
	}
	m.ModelLoadKnown = known
	m.ModelLoaded = loaded
	m.ModelLoadFailed = failed
	f.metrics[key] = m
}

// SetModelLoading reports that the model-load probe is still
// waiting for the model to finish loading into the serving
// container. The gate treats this as pending, never a failure.
func (f *FakeCluster) SetModelLoading(key string) {
	f.setModelLoad(key, true, false, false)
}

// SetModelLoaded reports that the model-load probe saw the model
// finish loading. The gate then judges the canary on readiness
// and traffic signals as usual.
func (f *FakeCluster) SetModelLoaded(key string) {
	f.setModelLoad(key, true, true, false)
}

// SetModelLoadFailed reports that the model-load probe saw the
// model fail to load (bad artifact, OOM, missing weights). The
// gate treats this as a real failure, so the rollout aborts
// after three strikes instead of waiting forever.
func (f *FakeCluster) SetModelLoadFailed(key string) {
	f.setModelLoad(key, true, false, true)
}

// ReadyReplicas implements Cluster.
func (f *FakeCluster) ReadyReplicas(key string) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready[key]
}

// SetReady simulates pods passing the readiness gate for the
// Deployment at key. It is a no-op for a Deployment the fake has
// never seen — readiness only makes sense for applied objects.
func (f *FakeCluster) SetReady(key string, ready int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.deployments[key]; ok {
		f.ready[key] = ready
	}
}

// ClusterSnapshot is the persistable state of a FakeCluster: every
// object it holds plus the readiness and load observations. The mlop
// CLI saves it to the state file so separate invocations share one
// simulated cluster. All maps are deep-copied on snapshot and
// restore, so later applies never corrupt a saved snapshot.
type ClusterSnapshot struct {
	Deployments map[string]render.Deployment
	Services    map[string]render.Service
	HPAs        map[string]render.HPA
	PDBs        map[string]render.PodDisruptionBudget
	Metrics     map[string]ClusterMetrics
	Ready       map[string]int32
}

// Snapshot returns a deep copy of the cluster's current state.
func (f *FakeCluster) Snapshot() ClusterSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := ClusterSnapshot{
		Deployments: make(map[string]render.Deployment, len(f.deployments)),
		Services:    make(map[string]render.Service, len(f.services)),
		HPAs:        make(map[string]render.HPA, len(f.hpas)),
		PDBs:        make(map[string]render.PodDisruptionBudget, len(f.pdbs)),
		Metrics:     make(map[string]ClusterMetrics, len(f.metrics)),
		Ready:       make(map[string]int32, len(f.ready)),
	}
	for k, v := range f.deployments {
		snap.Deployments[k] = v
	}
	for k, v := range f.services {
		snap.Services[k] = v
	}
	for k, v := range f.hpas {
		snap.HPAs[k] = v
	}
	for k, v := range f.pdbs {
		snap.PDBs[k] = v
	}
	for k, v := range f.metrics {
		snap.Metrics[k] = v
	}
	for k, v := range f.ready {
		snap.Ready[k] = v
	}
	return snap
}

// Restore replaces the cluster's state with a snapshot loaded from
// the state file. Nil maps in the snapshot become empty maps.
func (f *FakeCluster) Restore(snap ClusterSnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployments = copyDeploymentMap(snap.Deployments)
	f.services = copyServiceMap(snap.Services)
	f.hpas = copyHPAMap(snap.HPAs)
	f.pdbs = copyPDBMap(snap.PDBs)
	f.metrics = copyMetricsMap(snap.Metrics)
	f.ready = copyReadyMap(snap.Ready)
}

func copyDeploymentMap(in map[string]render.Deployment) map[string]render.Deployment {
	out := make(map[string]render.Deployment, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyServiceMap(in map[string]render.Service) map[string]render.Service {
	out := make(map[string]render.Service, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyHPAMap(in map[string]render.HPA) map[string]render.HPA {
	out := make(map[string]render.HPA, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyPDBMap(in map[string]render.PodDisruptionBudget) map[string]render.PodDisruptionBudget {
	out := make(map[string]render.PodDisruptionBudget, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyMetricsMap(in map[string]ClusterMetrics) map[string]ClusterMetrics {
	out := make(map[string]ClusterMetrics, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyReadyMap(in map[string]int32) map[string]int32 {
	out := make(map[string]int32, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// DeploymentCount returns how many Deployments the fake currently holds.
func (f *FakeCluster) DeploymentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deployments)
}

// ServiceCount returns how many Services the fake currently holds.
func (f *FakeCluster) ServiceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.services)
}

// HPACount returns how many HPAs the fake currently holds.
func (f *FakeCluster) HPACount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hpas)
}

// PDBCount returns how many PodDisruptionBudgets the fake currently holds.
func (f *FakeCluster) PDBCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pdbs)
}
