// Package render turns a ModelDeployment into the plain-Go objects
// the reconciler applies to the cluster: a Deployment, a Service,
// and (for canary rollouts) a second, small canary Deployment, or
// (for blue-green rollouts) a second, full-size green Deployment.
// The types here are dependency-free value structs — no client-go —
// so rendering and diffing stay testable without a cluster. The
// Kubernetes-backed types land with the controller-runtime phase.
package render

import (
	"fmt"
	"sort"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/rollout"
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

// PodSecurityContext is the pod-level security context rendered
// onto a Deployment's pod template: the uid every container runs
// as, whether the kubelet must refuse root, and the seccomp
// profile. It travels with the pod template, so canary and green
// Deployments inherit it from DeploymentFor.
type PodSecurityContext struct {
	RunAsNonRoot   bool
	RunAsUser      int64
	SeccompProfile string // "RuntimeDefault", "Unconfined", "Localhost/<path>"
}

// SecurityContext is the container-level security context for
// the model container: a read-only root filesystem, no privilege
// escalation, and the dropped Linux capabilities.
type SecurityContext struct {
	ReadOnlyRootFilesystem   bool
	AllowPrivilegeEscalation bool
	DropCapabilities         []string
}

// VolumeMount attaches one volume into a container at a
// path. The model-source volume mounts the same emptyDir
// into the init container (where the artifact is written)
// and the serving container (where it is read).
type VolumeMount struct {
	Name      string
	MountPath string
}

// Volume is one pod volume. Only emptyDir volumes exist in
// this operator so far: the shared model-artifact volume has
// no other kind to be.
type Volume struct {
	Name     string
	EmptyDir bool
}

// Container is the model-serving container of a rendered Deployment.
type Container struct {
	Name    string
	Image   string
	Port    int32
	Env     []EnvVar // sorted by name, so rendering is deterministic
	Command []string // empty for the serving container (image default)
	// VolumeMounts attaches shared volumes into the container;
	// nil for a container with no volumes to mount.
	VolumeMounts   []VolumeMount
	CPURequest     string
	CPULimit       string
	MemoryRequest  string
	MemoryLimit    string
	ReadinessProbe Probe
	Security       SecurityContext
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
	Canary bool
	// Green marks the extra full-size Deployment of a blue-green
	// rollout; the stable Deployment always has Green false.
	Green     bool
	Container Container
	// AntiAffinity, when true, spreads this Deployment's pods
	// across nodes (preferred pod anti-affinity on the hostname
	// topology key). Off only when the spec explicitly disables
	// it — the operator default is on.
	AntiAffinity bool
	// PodSecurity is the pod-level security context rendered
	// onto the pod template. Security is a property of the
	// model, not of one revision: canary and green Deployments
	// inherit it from DeploymentFor, so a rollout never
	// silently drops the hardening.
	PodSecurity PodSecurityContext
	// TopologySpread holds the topology-spread constraints for
	// the pod template; empty when the Deployment has a single
	// replica (nothing to spread).
	TopologySpread []TopologySpreadConstraint
	// NodeSelector pins the pod template's pods to nodes
	// carrying these labels; nil means unconstrained. Like
	// PodSecurity, placement is a property of the model, not
	// of one revision: canary and green Deployments inherit
	// it from DeploymentFor, so a rollout never silently
	// moves the model off its nodes.
	NodeSelector map[string]string
	// Tolerations lists the node taints the pod template's
	// pods tolerate; nil means the pods tolerate nothing.
	Tolerations []api.Toleration
	// ImagePullSecrets names the secrets holding registry
	// credentials for pulling the serving image; nil means
	// the pods pull without credentials. Like placement, it
	// is a property of the model, not of one revision:
	// canary and green Deployments inherit it from
	// DeploymentFor, so a rollout never fails to pull the
	// new image for want of credentials.
	ImagePullSecrets []string
	// InitContainers run to completion before any serving
	// container starts. With a model source set, the single
	// entry is the model-download container that fetches the
	// artifact into the shared volume; without one, empty.
	InitContainers []Container
	// Volumes holds the pod volumes. With a model source set,
	// the single entry is the shared emptyDir the init
	// container writes and the serving container reads;
	// without one, empty.
	Volumes []Volume
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

// HPA is the rendered HorizontalPodAutoscaler for a model: it scales
// the serving Deployment between MinReplicas and MaxReplicas. A
// target of 0 means that signal is disabled. The operator drives
// the same decisions in-process (internal/autoscale) so the demo
// and tests scale without a metrics server; the HPA object is what
// a real cluster applies, and both must agree on the spec.
type HPA struct {
	Name      string
	Namespace string
	Labels    map[string]string
	// MinReplicas / MaxReplicas are the scaling bounds from the spec.
	MinReplicas int32
	MaxReplicas int32
	// TargetCPUUtilization is the average CPU percentage target (0
	// disables CPU-based scaling).
	TargetCPUUtilization int32
	// TargetRequestsPerSecond is the per-pod request-rate target (0
	// disables RPS-based scaling).
	TargetRequestsPerSecond int32
}

// PodDisruptionBudget is the rendered PDB for a model: it keeps at
// least MinAvailable pods serving through voluntary disruptions
// (node drains, upgrades). The selector is the model's base
// selector, so it covers stable, canary and green pods alike —
// availability is a property of the model, not of one revision,
// and a canary pod counts toward the available count while it
// serves. Callers must run api.ApplyDefaults first so the
// availability block exists.
type PodDisruptionBudget struct {
	Name         string
	Namespace    string
	Labels       map[string]string
	Selector     map[string]string
	MinAvailable int32
}

// TopologySpreadConstraint is one pod topology-spread rule for a
// rendered Deployment: at most MaxSkew more pods on the busiest
// node than on the quietest one. WhenUnsatisfiable is always
// "ScheduleAnyway": spreading is a preference, never a reason to
// leave a model pod unscheduled when the cluster is tight.
type TopologySpreadConstraint struct {
	MaxSkew           int32
	TopologyKey       string // "kubernetes.io/hostname"
	WhenUnsatisfiable string // "ScheduleAnyway"
	LabelSelector     map[string]string
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
	// Stable pods carry canary=false and the selector requires it,
	// so the stable Deployment can never adopt canary pods: the two
	// Deployments' selectors are disjoint even though both share
	// the base labels the Service selects on.
	labels["ml-operator/canary"] = "false"
	// Stable pods also carry green=false, for the same reason in
	// the other direction: a blue-green rollout's green Deployment
	// selects green pods only, and the Service (which selects
	// green=false) never routes to green pods until the cutover.
	labels["ml-operator/green"] = "false"
	dep := Deployment{
		Name:      d.Name,
		Namespace: d.Namespace,
		Replicas:  replicas,
		Labels:    labels,
		Selector:  deploymentSelector(d, "false", "false"),
		Canary:    false,
		Container: containerFor(d),
		// Security resolves through the Effective* helpers, so a
		// spec that was never defaulted still renders the
		// hardened set rather than a zero value (root uid 0,
		// writable rootfs) — the safe direction.
		PodSecurity: PodSecurityContext{
			RunAsNonRoot:   d.Spec.Security.RunAsNonRootEnabled(),
			RunAsUser:      d.Spec.Security.EffectiveRunAsUser(),
			SeccompProfile: d.Spec.Security.EffectiveSeccompProfile(),
		},
		// Placement has no defaults to resolve, so it renders
		// as written — but the map and slice are copied, so a
		// caller mutating the spec afterwards cannot silently
		// move a Deployment that was already rendered.
		NodeSelector: copyStringMap(placementOf(d).NodeSelector),
		Tolerations:  copyTolerations(placementOf(d).Tolerations),
		// Pull secrets render as written too, and are copied
		// for the same reason: a caller appending to the spec's
		// slice afterwards must not change a Deployment that
		// was already rendered.
		ImagePullSecrets: copyStringSlice(d.Spec.ImagePullSecrets),
	}
	// Hardening travels with the pod template: anti-affinity and
	// the spread constraint apply to canary and green Deployments
	// too, since they derive from this one. A single replica has
	// nothing to spread, so the constraint is omitted there —
	// the PDB render makes the same call for the same reason.
	dep.AntiAffinity = d.Spec.Availability.AntiAffinityEnabled()
	if replicas > 1 {
		dep.TopologySpread = []TopologySpreadConstraint{{
			MaxSkew:           d.Spec.Availability.EffectiveMaxSkew(),
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: "ScheduleAnyway",
			LabelSelector:     Selector(d),
		}}
	}
	// A model source adds the download init container and the
	// shared emptyDir volume: the init container writes the
	// artifact, the serving container reads it. Canary and
	// green Deployments derive from this one, so they inherit
	// both — a rollout never serves without its artifact.
	if d.Spec.ModelSource != nil {
		dep.InitContainers = []Container{modelSourceInitContainer(d)}
		dep.Volumes = []Volume{{Name: modelSourceVolumeName, EmptyDir: true}}
	}
	return dep
}

// CanaryDeploymentFor renders the small canary Deployment for a
// canary rollout at the weight from the spec (default 10%): same
// image and container as the stable Deployment, but named
// "<name>-canary", labelled as a canary, and sized to the rollout
// weight (at least 1 replica). The canary Deployment selects
// canary pods only; the stable Deployment selects stable pods only.
// The Service selector matches both, so traffic splits by ready-pod
// count — the weight ladder in internal/rollout is the traffic
// split, and promotion folds the new image into the stable
// Deployment and deletes the canary.
func CanaryDeploymentFor(d api.ModelDeployment) Deployment {
	weight := int32(10)
	if d.Spec.Canary != nil && d.Spec.Canary.Weight > 0 {
		weight = d.Spec.Canary.Weight
	}
	return CanaryDeploymentForWeight(d, weight)
}

// CanaryDeploymentForWeight renders the canary Deployment at an
// explicit traffic weight, for the controller stepping a rollout
// along its weight ladder. The weight sizes the canary by
// rollout.CanaryReplicas, so the rendered replica count and the
// rollout state can never disagree.
func CanaryDeploymentForWeight(d api.ModelDeployment, weight int32) Deployment {
	dep := DeploymentFor(d)
	total := int32(1)
	if d.Spec.Replicas != nil {
		total = *d.Spec.Replicas
	}
	dep.Name = d.Name + "-canary"
	dep.Replicas = rollout.CanaryReplicas(total, weight)
	dep.Canary = true
	dep.Labels["ml-operator/canary"] = "true"
	dep.Selector = deploymentSelector(d, "true", "false")
	return dep
}

// GreenDeploymentFor renders the full-size green Deployment for a
// blue-green rollout: same image and container as the stable
// Deployment, but named "<name>-green", labelled as green, and
// sized to the full replica count — green serves the whole load
// after the cutover, so it soaks at the size it will serve at. The
// green Deployment selects green pods only; the stable Deployment
// selects blue pods only. The Service selector matches blue pods
// until the cutover, then green pods, so traffic never splits.
func GreenDeploymentFor(d api.ModelDeployment) Deployment {
	dep := DeploymentFor(d)
	total := int32(1)
	if d.Spec.Replicas != nil {
		total = *d.Spec.Replicas
	}
	dep.Name = d.Name + "-green"
	dep.Replicas = total
	dep.Green = true
	dep.Labels["ml-operator/green"] = "true"
	dep.Selector = deploymentSelector(d, "false", "true")
	return dep
}

// deploymentSelector is the Deployment selector for a model's pods:
// the base selector plus the canary and green labels, so the
// stable, canary and green Deployments never select each other's
// pods.
func deploymentSelector(d api.ModelDeployment, canary, green string) map[string]string {
	sel := Selector(d)
	sel["ml-operator/canary"] = canary
	sel["ml-operator/green"] = green
	return sel
}

// ServiceFor renders the ClusterIP Service fronting d. Clients
// connect on port 80; traffic is forwarded to the container port.
// The selector is the base selector plus green=false, so during a
// canary rollout it matches both stable and canary pods (canary
// pods carry green=false) and traffic splits by ready-pod count —
// the weight ladder in internal/rollout sizes the canary, which is
// what sets the split. During a blue-green rollout it matches only
// blue pods: green pods carry green=true and get no traffic until
// the cutover flips the Service to GreenServiceFor.
func ServiceFor(d api.ModelDeployment) Service {
	return Service{
		Name:       d.Name,
		Namespace:  d.Namespace,
		Labels:     Labels(d),
		Selector:   serviceSelector(d, "false"),
		Port:       80,
		TargetPort: d.Spec.Port,
	}
}

// GreenServiceFor renders the Service as it looks after a
// blue-green cutover: same as ServiceFor, but the selector requires
// green=true, so clients reach the green Deployment. The reconciler
// flips to this Service when the soak gate passes, then folds the
// green image into the stable Deployment and flips back.
func GreenServiceFor(d api.ModelDeployment) Service {
	svc := ServiceFor(d)
	svc.Selector = serviceSelector(d, "true")
	return svc
}

// serviceSelector is the Service selector for a model's pods: the
// base selector plus the green label. The canary label is
// deliberately absent — the Service must select both stable and
// canary pods during a canary rollout.
func serviceSelector(d api.ModelDeployment, green string) map[string]string {
	sel := Selector(d)
	sel["ml-operator/green"] = green
	return sel
}

// HPAFor renders the HorizontalPodAutoscaler for d, or false when
// autoscaling is disabled — no HPA object exists then, and the
// reconciler garbage-collects any leftover one. Callers must run
// api.ApplyDefaults first so MinReplicas is filled in.
func HPAFor(d api.ModelDeployment) (HPA, bool) {
	if d.Spec.Autoscaling == nil {
		return HPA{}, false
	}
	a := d.Spec.Autoscaling
	return HPA{
		Name:                    d.Name,
		Namespace:               d.Namespace,
		Labels:                  Labels(d),
		MinReplicas:             a.MinReplicas,
		MaxReplicas:             a.MaxReplicas,
		TargetCPUUtilization:    a.TargetCPUUtilization,
		TargetRequestsPerSecond: a.TargetRequestsPerSecond,
	}, true
}

// PDBFor renders the PodDisruptionBudget for d, or false when the
// model has nothing to protect: a single replica resolves to an
// effective minAvailable of 0, and a PDB that allows every
// disruption protects nothing, so the operator renders none and
// `mlop diff` stays clean for the default one-replica deployment.
func PDBFor(d api.ModelDeployment) (PodDisruptionBudget, bool) {
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	min := d.Spec.Availability.EffectiveMinAvailable(replicas)
	if min <= 0 {
		return PodDisruptionBudget{}, false
	}
	return PodDisruptionBudget{
		Name:         d.Name,
		Namespace:    d.Namespace,
		Labels:       Labels(d),
		Selector:     Selector(d),
		MinAvailable: min,
	}, true
}

// PDBDiffReasons compares the fields the reconciler manages on a
// PDB and returns a human-readable reason per difference, or nil
// when the live PDB already matches the desired one.
func PDBDiffReasons(desired, live PodDisruptionBudget) []string {
	var reasons []string
	if desired.MinAvailable != live.MinAvailable {
		reasons = append(reasons, fmt.Sprintf("minAvailable %d -> %d", live.MinAvailable, desired.MinAvailable))
	}
	if !labelsEqual(desired.Selector, live.Selector) {
		reasons = append(reasons, "selector changed")
	}
	return reasons
}

// HPADiffReasons compares the fields the reconciler manages on an
// HPA and returns a human-readable reason per difference, or nil
// when the live HPA already matches the desired one.
func HPADiffReasons(desired, live HPA) []string {
	var reasons []string
	if desired.MinReplicas != live.MinReplicas {
		reasons = append(reasons, fmt.Sprintf("minReplicas %d -> %d", live.MinReplicas, desired.MinReplicas))
	}
	if desired.MaxReplicas != live.MaxReplicas {
		reasons = append(reasons, fmt.Sprintf("maxReplicas %d -> %d", live.MaxReplicas, desired.MaxReplicas))
	}
	if desired.TargetCPUUtilization != live.TargetCPUUtilization {
		reasons = append(reasons, fmt.Sprintf("targetCPUUtilization %d -> %d", live.TargetCPUUtilization, desired.TargetCPUUtilization))
	}
	if desired.TargetRequestsPerSecond != live.TargetRequestsPerSecond {
		reasons = append(reasons, fmt.Sprintf("targetRequestsPerSecond %d -> %d", live.TargetRequestsPerSecond, desired.TargetRequestsPerSecond))
	}
	return reasons
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
	if desired.AntiAffinity != live.AntiAffinity {
		reasons = append(reasons, fmt.Sprintf("antiAffinity %v -> %v", live.AntiAffinity, desired.AntiAffinity))
	}
	if !topologySpreadEqual(desired.TopologySpread, live.TopologySpread) {
		reasons = append(reasons, "topology spread changed")
	}
	// Security is rendered onto every Deployment, so a security
	// change is a Deployment change: the reconciler re-applies
	// and `mlop diff` names it, instead of the drift check
	// silently reporting the Deployment in sync.
	if desired.PodSecurity.RunAsNonRoot != live.PodSecurity.RunAsNonRoot {
		reasons = append(reasons, fmt.Sprintf("runAsNonRoot %v -> %v", live.PodSecurity.RunAsNonRoot, desired.PodSecurity.RunAsNonRoot))
	}
	if desired.PodSecurity.RunAsUser != live.PodSecurity.RunAsUser {
		reasons = append(reasons, fmt.Sprintf("runAsUser %d -> %d", live.PodSecurity.RunAsUser, desired.PodSecurity.RunAsUser))
	}
	if desired.PodSecurity.SeccompProfile != live.PodSecurity.SeccompProfile {
		reasons = append(reasons, fmt.Sprintf("seccompProfile %q -> %q", live.PodSecurity.SeccompProfile, desired.PodSecurity.SeccompProfile))
	}
	dc, lc := desired.Container.Security, live.Container.Security
	if dc.ReadOnlyRootFilesystem != lc.ReadOnlyRootFilesystem {
		reasons = append(reasons, fmt.Sprintf("readOnlyRootFilesystem %v -> %v", lc.ReadOnlyRootFilesystem, dc.ReadOnlyRootFilesystem))
	}
	if dc.AllowPrivilegeEscalation != lc.AllowPrivilegeEscalation {
		reasons = append(reasons, fmt.Sprintf("allowPrivilegeEscalation %v -> %v", lc.AllowPrivilegeEscalation, dc.AllowPrivilegeEscalation))
	}
	if !stringSliceEqual(dc.DropCapabilities, lc.DropCapabilities) {
		reasons = append(reasons, "dropCapabilities changed")
	}
	// Placement is rendered onto every Deployment, so a
	// placement change is a Deployment change: the reconciler
	// re-applies and `mlop diff` names it, instead of the drift
	// check silently reporting the Deployment in sync.
	if !labelsEqual(desired.NodeSelector, live.NodeSelector) {
		reasons = append(reasons, "nodeSelector changed")
	}
	if !tolerationsEqual(desired.Tolerations, live.Tolerations) {
		reasons = append(reasons, "tolerations changed")
	}
	// A model source is rendered onto every Deployment too:
	// a new, removed or changed source changes the init
	// container, the shared volume and the serving
	// container's mount, so the reconciler must re-apply
	// and `mlop diff` must name it.
	if !initContainersEqual(desired.InitContainers, live.InitContainers) {
		reasons = append(reasons, "model download init container changed")
	}
	if !volumesEqual(desired.Volumes, live.Volumes) {
		reasons = append(reasons, "model artifact volume changed")
	}
	if !volumeMountsEqual(desired.Container.VolumeMounts, live.Container.VolumeMounts) {
		reasons = append(reasons, "model artifact mount changed")
	}
	// Pull secrets are rendered onto every Deployment too:
	// adding, removing or rotating one changes the pod
	// template, so the reconciler must re-apply and `mlop
	// diff` must name it.
	if !stringSliceEqual(desired.ImagePullSecrets, live.ImagePullSecrets) {
		reasons = append(reasons, "imagePullSecrets changed")
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
	// A model source mounts the shared volume into the
	// serving container and tells it where the artifact
	// landed. The author's own MODEL_ARTIFACT_PATH wins when
	// set: the operator must not silently override an
	// explicit path.
	var mounts []VolumeMount
	if d.Spec.ModelSource != nil {
		m := d.Spec.ModelSource
		mounts = []VolumeMount{{Name: modelSourceVolumeName, MountPath: m.EffectiveMountPath()}}
		if !envHas(env, "MODEL_ARTIFACT_PATH") {
			env = append(env, EnvVar{Name: "MODEL_ARTIFACT_PATH", Value: m.ArtifactPath()})
			sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
		}
	}
	return Container{
		Name:          "model",
		Image:         d.Spec.Model.Image,
		Port:          d.Spec.Port,
		Env:           env,
		VolumeMounts:  mounts,
		CPURequest:    d.Spec.Resources.CPURequest,
		CPULimit:      d.Spec.Resources.CPULimit,
		MemoryRequest: d.Spec.Resources.MemoryRequest,
		MemoryLimit:   d.Spec.Resources.MemoryLimit,
		ReadinessProbe: Probe{
			Path:                d.Spec.Health.Path,
			InitialDelaySeconds: d.Spec.Health.InitialDelaySeconds,
			PeriodSeconds:       d.Spec.Health.PeriodSeconds,
		},
		Security: SecurityContext{
			ReadOnlyRootFilesystem:   d.Spec.Security.ReadOnlyRootFilesystemEnabled(),
			AllowPrivilegeEscalation: d.Spec.Security.AllowPrivilegeEscalationEnabled(),
			DropCapabilities:         d.Spec.Security.EffectiveDropCapabilities(),
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

// stringSliceEqual compares two string slices by content and order.
func stringSliceEqual(a, b []string) bool {
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

func topologySpreadEqual(a, b []TopologySpreadConstraint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].MaxSkew != b[i].MaxSkew ||
			a[i].TopologyKey != b[i].TopologyKey ||
			a[i].WhenUnsatisfiable != b[i].WhenUnsatisfiable ||
			!labelsEqual(a[i].LabelSelector, b[i].LabelSelector) {
			return false
		}
	}
	return true
}

// placementOf returns the placement spec, or an empty one when the
// deployment is unconstrained, so renderers never nil-dereference
// a nil Placement pointer.
func placementOf(d api.ModelDeployment) api.PlacementSpec {
	if d.Spec.Placement == nil {
		return api.PlacementSpec{}
	}
	return *d.Spec.Placement
}

// modelSourceVolumeName is the shared emptyDir volume the init
// container writes the artifact into and the serving container
// reads it from.
const modelSourceVolumeName = "model-artifacts"

// modelSourceInitContainer renders the download init container
// for a sourced deployment: it fetches the artifact from the
// source URI into the shared volume before the serving
// container starts, so a pod never serves with a missing or
// half-downloaded model. The URI, checksum and destination
// travel as environment variables, so no value is ever
// interpolated into the shell command.
func modelSourceInitContainer(d api.ModelDeployment) Container {
	m := d.Spec.ModelSource
	env := []EnvVar{
		{Name: "MODEL_SOURCE_URI", Value: m.URI},
		{Name: "MODEL_ARTIFACT_PATH", Value: m.ArtifactPath()},
	}
	if m.SHA256 != "" {
		env = append(env, EnvVar{Name: "MODEL_SOURCE_SHA256", Value: m.SHA256})
	}
	return Container{
		Name:         "model-download",
		Image:        m.EffectiveDownloaderImage(),
		Command:      []string{"sh", "-c", modelSourceDownloadScript(m.SHA256 != "")},
		Env:          env,
		VolumeMounts: []VolumeMount{{Name: modelSourceVolumeName, MountPath: m.EffectiveMountPath()}},
	}
}

// modelSourceDownloadScript is the init container's command:
// fetch the artifact with curl (fail on HTTP errors, follow
// redirects), then verify the checksum when one is configured
// — a failed verification fails the init container, so the pod
// restarts the download instead of serving a bad artifact.
// Without a checksum the download is unverified by design
// (Lint warns about it at apply time).
func modelSourceDownloadScript(verified bool) string {
	script := `set -eu; curl -fsSL "$MODEL_SOURCE_URI" -o "$MODEL_ARTIFACT_PATH"`
	if verified {
		script += `; echo "$MODEL_SOURCE_SHA256  $MODEL_ARTIFACT_PATH" | sha256sum -c -`
	}
	return script
}

// envHas reports whether an environment variable of the given
// name is already set.
func envHas(env []EnvVar, name string) bool {
	for _, e := range env {
		if e.Name == name {
			return true
		}
	}
	return false
}

// initContainersEqual compares init containers by the fields
// the operator renders: name, image, command, env and volume
// mounts. A change in any of them means the pod's startup
// behaviour changed.
func initContainersEqual(a, b []Container) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Image != b[i].Image ||
			!stringSliceEqual(a[i].Command, b[i].Command) ||
			!envEqual(a[i].Env, b[i].Env) ||
			!volumeMountsEqual(a[i].VolumeMounts, b[i].VolumeMounts) {
			return false
		}
	}
	return true
}

// volumesEqual compares pod volumes by name and kind.
func volumesEqual(a, b []Volume) bool {
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

// volumeMountsEqual compares volume mounts by name and path.
func volumeMountsEqual(a, b []VolumeMount) bool {
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

// copyStringMap copies a label map, or nil when the input is nil,
// so a rendered Deployment never aliases the spec's map.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// copyStringSlice copies a string list, or nil when the input
// is nil, so a rendered Deployment never aliases the spec's
// slice — a later append to the spec must not change a
// Deployment that was already rendered.
func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// copyTolerations copies a toleration list, or nil when the input
// is nil. TolerationSeconds is a pointer, so its value is copied
// too — a later mutation of the spec's seconds must not move a
// Deployment that was already rendered.
func copyTolerations(in []api.Toleration) []api.Toleration {
	if in == nil {
		return nil
	}
	out := make([]api.Toleration, len(in))
	copy(out, in)
	for i := range out {
		if in[i].TolerationSeconds != nil {
			s := *in[i].TolerationSeconds
			out[i].TolerationSeconds = &s
		}
	}
	return out
}

// tolerationsEqual compares two toleration lists by value: the
// operator, key, value, effect and seconds of every entry, in
// order. A plain slice comparison would compare the
// TolerationSeconds pointers, not the seconds, so a spec that
// survived a JSON round-trip (state file) would never equal a
// freshly parsed manifest with the same tolerations.
func tolerationsEqual(a, b []api.Toleration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Operator != b[i].Operator ||
			a[i].Value != b[i].Value || a[i].Effect != b[i].Effect {
			return false
		}
		if (a[i].TolerationSeconds == nil) != (b[i].TolerationSeconds == nil) {
			return false
		}
		if a[i].TolerationSeconds != nil && *a[i].TolerationSeconds != *b[i].TolerationSeconds {
			return false
		}
	}
	return true
}
