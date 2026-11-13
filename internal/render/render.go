// Package render turns a ModelDeployment into the plain-Go objects
// the reconciler applies to the cluster: a Deployment, a Service,
// and (for canary rollouts) a second, small canary Deployment. The
// types here are dependency-free value structs — no client-go — so
// rendering and diffing stay testable without a cluster. The
// Kubernetes-backed types land with the controller-runtime phase.
package render

import (
	"fmt"
	"sort"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// ManagedBy is the label value marking every object this operator owns.
const ManagedBy = "ml-operator"

// EnvVar is one container environment variable.
type EnvVar struct {
	Name  string
	Value string
}

// Probe is a container HTTP readiness probe.
type Probe struct {
	Path                string
	InitialDelaySeconds int32
	PeriodSeconds       int32
}

// Container is the model-serving container of a rendered Deployment.
type Container struct {
	Name           string
	Image          string
	Port           int32
	Env            []EnvVar // sorted by name, so rendering is deterministic
	CPURequest     string
	CPULimit       string
	MemoryRequest  string
	MemoryLimit    string
	ReadinessProbe Probe
}

// Deployment is the rendered serving Deployment for a model.
type Deployment struct {
	Name      string
	Namespace string
	Replicas  int32
	// Labels are the pod-template labels; Selector is the Deployment
	// selector and must stay stable across revisions (it never
	// carries the version label, or an update would orphan pods).
	Labels   map[string]string
	Selector map[string]string
	// Canary marks the extra small Deployment of a canary rollout;
	// the stable Deployment always has Canary false.
	Canary    bool
	Container Container
}

// Service is the rendered ClusterIP Service fronting a model.
type Service struct {
	Name       string
	Namespace  string
	Labels     map[string]string
	Selector   map[string]string
	Port       int32 // the Service port clients connect to
	TargetPort int32 // the container port (spec.Port)
}

// Labels returns the labels identifying a model's objects. The
// version label lives on pod templates only, never on selectors.
func Labels(d api.ModelDeployment) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": d.Name,
		"ml-operator/model":      d.Name,
		"ml-operator/managed-by": ManagedBy,
	}
}

// Selector returns the stable selector labels for a model's objects.
// It is a subset of Labels: the version is deliberately excluded so
// a version bump never changes the selector.
func Selector(d api.ModelDeployment) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": d.Name,
		"ml-operator/managed-by": ManagedBy,
	}
}

// DeploymentFor renders the stable serving Deployment for d. Callers
// must run api.ApplyDefaults first; zero fields here render as-is
// (a zero Port means the manifest was never defaulted).
func DeploymentFor(d api.ModelDeployment) Deployment {
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	labels := Labels(d)
	labels["ml-operator/version"] = d.Spec.Model.Version
	return Deployment{
		Name:      d.Name,
		Namespace: d.Namespace,
		Replicas:  replicas,
		Labels:    labels,
		Selector:  Selector(d),
		Canary:    false,
		Container: containerFor(d),
	}
}

// CanaryDeploymentFor renders the small canary Deployment for a
// canary rollout: same image and container as the stable Deployment,
// but named "<name>-canary", labelled as a canary, and sized to the
// rollout weight (at least 1 replica). The stable Service selector
// never matches canary pods, so no traffic reaches the canary until
// the rollout session adds traffic shifting.
func CanaryDeploymentFor(d api.ModelDeployment) Deployment {
	dep := DeploymentFor(d)
	weight := int32(10)
	if d.Spec.Canary != nil && d.Spec.Canary.Weight > 0 {
		weight = d.Spec.Canary.Weight
	}
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		// Round to nearest, minimum 1: a 10% canary of 4 replicas is 1.
		replicas = (*d.Spec.Replicas*weight + 50) / 100
		if replicas < 1 {
			replicas = 1
		}
	}
	dep.Name = d.Name + "-canary"
	dep.Replicas = replicas
	dep.Canary = true
	dep.Labels["ml-operator/canary"] = "true"
	return dep
}

// ServiceFor renders the ClusterIP Service fronting d. Clients
// connect on port 80; traffic is forwarded to the container port.
func ServiceFor(d api.ModelDeployment) Service {
	return Service{
		Name:       d.Name,
		Namespace:  d.Namespace,
		Labels:     Labels(d),
		Selector:   Selector(d),
		Port:       80,
		TargetPort: d.Spec.Port,
	}
}

// DeploymentDiffReasons compares the fields the reconciler manages
// and returns a human-readable reason per difference, or nil when
// the live Deployment already matches the desired one. Fields the
// operator does not own (e.g. cluster-assigned IPs) are ignored.
func DeploymentDiffReasons(desired, live Deployment) []string {
	var reasons []string
	if desired.Replicas != live.Replicas {
		reasons = append(reasons, fmt.Sprintf("replicas %d -> %d", live.Replicas, desired.Replicas))
	}
	if desired.Container.Image != live.Container.Image {
		reasons = append(reasons, fmt.Sprintf("image %q -> %q", live.Container.Image, desired.Container.Image))
	}
	if desired.Container.Port != live.Container.Port {
		reasons = append(reasons, fmt.Sprintf("port %d -> %d", live.Container.Port, desired.Container.Port))
	}
	if !envEqual(desired.Container.Env, live.Container.Env) {
		reasons = append(reasons, "env changed")
	}
	c, l := desired.Container, live.Container
	if c.CPURequest != l.CPURequest || c.CPULimit != l.CPULimit ||
		c.MemoryRequest != l.MemoryRequest || c.MemoryLimit != l.MemoryLimit {
		reasons = append(reasons, "resources changed")
	}
	if c.ReadinessProbe != l.ReadinessProbe {
		reasons = append(reasons, fmt.Sprintf("readiness probe %q -> %q", l.ReadinessProbe.Path, c.ReadinessProbe.Path))
	}
	return reasons
}

// ServiceDiffReasons compares the fields the reconciler manages on a
// Service and returns a human-readable reason per difference.
func ServiceDiffReasons(desired, live Service) []string {
	var reasons []string
	if desired.Port != live.Port {
		reasons = append(reasons, fmt.Sprintf("port %d -> %d", live.Port, desired.Port))
	}
	if desired.TargetPort != live.TargetPort {
		reasons = append(reasons, fmt.Sprintf("targetPort %d -> %d", live.TargetPort, desired.TargetPort))
	}
	if !labelsEqual(desired.Selector, live.Selector) {
		reasons = append(reasons, "selector changed")
	}
	return reasons
}

func containerFor(d api.ModelDeployment) Container {
	env := make([]EnvVar, 0, len(d.Spec.Env))
	for k, v := range d.Spec.Env {
		env = append(env, EnvVar{Name: k, Value: v})
	}
	sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
	return Container{
		Name:          "model",
		Image:         d.Spec.Model.Image,
		Port:          d.Spec.Port,
		Env:           env,
		CPURequest:    d.Spec.Resources.CPURequest,
		CPULimit:      d.Spec.Resources.CPULimit,
		MemoryRequest: d.Spec.Resources.MemoryRequest,
		MemoryLimit:   d.Spec.Resources.MemoryLimit,
		ReadinessProbe: Probe{
			Path:                d.Spec.Health.Path,
			InitialDelaySeconds: d.Spec.Health.InitialDelaySeconds,
			PeriodSeconds:       d.Spec.Health.PeriodSeconds,
		},
	}
}

func envEqual(a, b []EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
