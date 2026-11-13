package controller

import (
	"sync"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// FakeCluster is an in-memory Cluster for tests and the operator demo
// loop. It stores exactly what was applied — no rollout simulation —
// so a test controls readiness explicitly with SetReady, the way a
// real cluster would report it asynchronously after pods come up.
type FakeCluster struct {
	mu          sync.Mutex
	deployments map[string]render.Deployment
	services    map[string]render.Service
	ready       map[string]int32
}

// NewFakeCluster returns an empty fake cluster.
func NewFakeCluster() *FakeCluster {
	return &FakeCluster{
		deployments: make(map[string]render.Deployment),
		services:    make(map[string]render.Service),
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

// ApplyDeployment implements Cluster. A newly applied Deployment
// starts with 0 ready replicas — pods take time to come up — until
// the test (or demo) calls SetReady. Re-applying an existing
// Deployment does NOT reset readiness: the fake does not simulate
// pod restarts, so a test that needs a readiness drop after an
// update must call SetReady explicitly.
func (f *FakeCluster) ApplyDeployment(d render.Deployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := d.Namespace + "/" + d.Name
	if _, ok := f.deployments[key]; !ok {
		f.ready[key] = 0
	}
	f.deployments[key] = d
	return nil
}

// DeleteDeployment implements Cluster.
func (f *FakeCluster) DeleteDeployment(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.deployments, key)
	delete(f.ready, key)
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
	f.services[s.Namespace+"/"+s.Name] = s
	return nil
}

// DeleteService implements Cluster.
func (f *FakeCluster) DeleteService(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.services, key)
	return nil
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
