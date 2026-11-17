package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Phase is the lifecycle phase of a ModelDeployment, reported in status.
type Phase string

const (
	// PhasePending is the initial phase: the deployment is registered
	// but the operator has not started provisioning it yet.
	PhasePending Phase = "Pending"
	// PhaseProgressing means the operator is provisioning or rolling
	// out the serving Deployment.
	PhaseProgressing Phase = "Progressing"
	// PhaseAvailable means the desired replicas are ready and serving.
	PhaseAvailable Phase = "Available"
	// PhaseDegraded means the model is serving but below the desired
	// replica count, or a rollout is stuck behind a health gate.
	PhaseDegraded Phase = "Degraded"
	// PhaseFailed means the deployment could not be provisioned
	// (invalid spec that passed admission, image pull failure, ...).
	PhaseFailed Phase = "Failed"
)

// RolloutStrategy selects how a new model revision replaces the old one.
type RolloutStrategy string

const (
	// StrategyRecreate tears the old pods down before starting new ones.
	StrategyRecreate RolloutStrategy = "Recreate"
	// StrategyRolling replaces pods gradually (maxUnavailable 25%).
	StrategyRolling RolloutStrategy = "RollingUpdate"
	// StrategyCanary shifts a percentage of traffic to the new revision
	// and holds until the health gate passes.
	StrategyCanary RolloutStrategy = "Canary"
	// StrategyBlueGreen runs the new revision at full size beside
	// the old one, then flips the Service to it once the health
	// gate passes. Traffic never splits: clients see either the
	// old version or the new one, never both at once.
	StrategyBlueGreen RolloutStrategy = "BlueGreen"
)

// ModelDeployment is the custom resource the operator watches. It is
// deliberately plain Go (no client-go types) so the API package stays
// dependency-free and usable from the CLI and tests.
type ModelDeployment struct {
	// Name is the deployment name; must be a DNS-1123 subdomain.
	Name string
	// Namespace scopes the deployment; empty means "default".
	Namespace string
	// Generation is bumped by the API server on every spec change; the
	// operator records the last generation it acted on in status.
	Generation int64

	Spec   ModelDeploymentSpec
	Status ModelDeploymentStatus
}

// ModelDeploymentSpec is the desired state of a deployed model.
type ModelDeploymentSpec struct {
	// Model is the model artifact to serve.
	Model ModelSpec
	// Replicas is the desired pod count; nil means the default (1).
	// Autoscaling, when enabled, overrides this at runtime.
	Replicas *int32
	// Resources are the per-pod CPU/memory requests and limits.
	Resources ResourceRequirements
	// Port is the container port the model server listens on.
	Port int32
	// Strategy selects the rollout behaviour for new revisions.
	Strategy RolloutStrategy
	// Canary configures a canary rollout; required when Strategy is
	// Canary, ignored otherwise.
	Canary *CanarySpec
	// BlueGreen configures a blue-green rollout; optional when
	// Strategy is BlueGreen (a nil config takes the operator
	// defaults), ignored otherwise.
	BlueGreen *BlueGreenSpec
	// Autoscaling enables the HorizontalPodAutoscaler for this model.
	Autoscaling *AutoscalingSpec
	// Availability configures how the operator keeps the model
	// serving through voluntary disruptions (node drains,
	// upgrades) and spreads its pods across nodes. Nil means the
	// operator defaults: anti-affinity on, maxSkew 1, minAvailable
	// replicas-1.
	Availability *AvailabilitySpec
	// Security configures the pod- and container-level security
	// context the operator renders onto every serving
	// Deployment (stable, canary and green alike — security is
	// a property of the model, not of one revision). Nil means
	// the operator defaults: non-root uid 10001, read-only root
	// filesystem, no privilege escalation, all Linux
	// capabilities dropped, RuntimeDefault seccomp. A model
	// server that needs to write to its root filesystem or run
	// as root must say so explicitly, and `mlop validate` warns
	// about every relaxation.
	Security *SecuritySpec
	// Placement pins the model's pods to nodes: which nodes
	// they may run on (nodeSelector) and which node taints
	// they tolerate (tolerations). Nil means unconstrained:
	// the scheduler places the pods anywhere. This is how a
	// GPU model lands on a GPU node — GPU nodes are usually
	// tainted (so only GPU workloads schedule there) and
	// labelled (so the selector finds them) — and how a
	// latency-sensitive model stays off preemptible nodes.
	// Placement is a property of the model, not of one
	// revision: canary and green Deployments inherit it, so a
	// rollout never silently moves the model off its nodes.
	// It has no operator defaults to fill in: an absent
	// placement is a real choice (schedule anywhere), not an
	// omission, so ApplyDefaults leaves it alone.
	Placement *PlacementSpec
	// ModelSource fetches the model artifact into the pod at
	// startup, instead of baking the weights into the serving
	// image: an init container downloads the artifact from
	// URI into a shared emptyDir volume mounted at MountPath,
	// and the serving container finds the weights on disk.
	// Nil means the image already contains the model (the
	// original behaviour) and no init container is rendered.
	// ModelSource is a property of the model, not of one
	// revision: canary and green Deployments inherit it, so a
	// rollout never silently serves a stale or missing
	// artifact. It has no operator defaults to fill in
	// beyond the mount path and downloader image: an absent
	// source is a real choice (weights in the image), not an
	// omission, so ApplyDefaults leaves it alone.
	ModelSource *ModelSourceSpec
	// Paused suspends reconciliation for this deployment: while
	// true the operator makes no cluster changes, advances no
	// rollout, and applies no autoscaling decision — the live
	// objects keep serving exactly as they are. A paused
	// deployment keeps its phase and reports a Paused=True
	// condition; resuming (false) reconciles on the next
	// invocation and picks up any spec change that landed while
	// paused. Pausing mid-rollout freezes the rollout state, so
	// a canary or blue-green rollout resumes from where it
	// stopped instead of restarting. Apply replaces the whole
	// spec, so a manifest without paused resumes the deployment
	// (absent means false) — to change the spec while staying
	// paused, the manifest must set paused: true.
	Paused bool
	// MaintenanceWindow restricts when the operator may change
	// cluster objects for this deployment: applies, rollouts,
	// scaling and garbage collection only run while the window
	// is open. Outside the window a reconcile that would change
	// anything defers — status reports WindowBlocked and the
	// next window's opening — and picks up where it left off
	// when the window opens. A deployment that is already in
	// sync reconciles normally outside the window (there is
	// nothing to defer), and an explicit `mlop repair` still
	// applies immediately: repair is a human decision, not an
	// automatic change. Nil means unrestricted: the operator
	// may change things at any time. Times are UTC.
	MaintenanceWindow *MaintenanceWindowSpec
	// Env holds extra environment variables for the model container.
	Env map[string]string
	// Health configures the readiness gate for the model container.
	Health HealthSpec
}

// ModelSpec identifies the model artifact and serving runtime.
type ModelSpec struct {
	// Image is the container image serving the model
	// (e.g. "registry.example.com/iris-mlp:v2").
	Image string
	// Framework names the serving runtime (e.g. "onnx", "torchserve",
	// "triton", "custom"). Free-form; the operator only uses it for
	// the default readiness path when Health.Path is empty.
	Framework string
	// Version is the model version label recorded in status and metrics.
	Version string
}

// ResourceRequirements mirrors the Kubernetes requests/limits pair,
// with quantities as strings ("500m", "1Gi") so the API package has
// no resource-quantity dependency.
type ResourceRequirements struct {
	// CPURequest is the CPU request, e.g. "500m". Empty means unset.
	CPURequest string
	// CPULimit is the CPU limit, e.g. "2". Empty means unset.
	CPULimit string
	// MemoryRequest is the memory request, e.g. "512Mi".
	MemoryRequest string
	// MemoryLimit is the memory limit, e.g. "1Gi".
	MemoryLimit string
}

// CanarySpec configures a canary rollout.
type CanarySpec struct {
	// Weight is the initial percentage of traffic sent to the new
	// revision (1-99). The operator steps it up after each health
	// gate passes.
	Weight int32
	// Steps is the number of traffic increments to reach 100%.
	// 0 means a single step (Weight straight to 100% after the gate).
	Steps int32
	// IntervalSeconds is the pause between steps.
	IntervalSeconds int32
	// Gate overrides the health-gate thresholds for this rollout.
	// Nil means the operator defaults (5% errors, 500ms p95, all
	// canary pods ready). A rollout can loosen or tighten the gate
	// — a latency-sensitive model gets a lower p95 cap, a noisy
	// one a higher error budget — but a threshold never disables
	// its check: a zero field takes the default, it does not turn
	// the check off.
	Gate *GateSpec
}

// BlueGreenSpec configures a blue-green rollout.
type BlueGreenSpec struct {
	// Gate overrides the health-gate thresholds for this rollout.
	// Nil means the operator defaults (5% errors, 500ms p95, all
	// green pods ready). Like a canary gate, a threshold never
	// disables its check: a zero field takes the default, it does
	// not turn the check off.
	Gate *GateSpec
}

// GateSpec sets the health-gate thresholds for one canary rollout.
// It mirrors rollout.Gate, minus the "0 disables the check"
// semantics: here a zero field means "use the operator default",
// because ApplyDefaults fills every zero field before the gate is
// ever evaluated.
type GateSpec struct {
	// MaxErrorRate is the maximum tolerated error fraction (0-1).
	// 0 means the default (5%).
	MaxErrorRate float64
	// MaxP95LatencyMs is the maximum tolerated p95 latency in
	// milliseconds. 0 means the default (500ms).
	MaxP95LatencyMs float64
	// MinReadyRatio is the minimum ready/desired canary replicas
	// (0-1). 0 means the default (1.0, every canary pod ready).
	MinReadyRatio float64
}

// MaintenanceWindowSpec is a recurring weekly window, in UTC,
// during which the operator may change cluster objects for a
// deployment. Outside it, changes wait.
type MaintenanceWindowSpec struct {
	// Days lists the weekdays the window opens on, as
	// three-letter abbreviations (Mon, Tue, Wed, Thu, Fri,
	// Sat, Sun), case-insensitive. Empty means every day.
	Days []string
	// Start is the opening time, "HH:MM" in 24-hour UTC
	// (e.g. "02:00").
	Start string
	// End is the closing time, "HH:MM" in 24-hour UTC. End may
	// be earlier than Start: the window then runs overnight,
	// past midnight into the next day (22:00–02:00 opens at
	// 22:00 and closes at 02:00 the next morning). End equal
	// to Start is invalid — that window would never open.
	End string
}

// AvailabilitySpec configures how the operator keeps a model
// serving through voluntary disruptions and spreads its pods
// across failure domains.
type AvailabilitySpec struct {
	// MinAvailable is the number of pods that must stay available
	// during a voluntary disruption (node drain, upgrade). 0
	// means the default: one fewer than the replica count
	// (replicas-1), so a single pod can always be drained. It
	// must stay below the replica count: minAvailable equal to
	// the replica count would block every eviction. An explicit
	// 0 is the same as unset — the default applies — because a
	// deployment with one replica already resolves to 0.
	MinAvailable int32
	// AntiAffinity, when true, keeps pods of one deployment off
	// the same node (preferred pod anti-affinity on the hostname
	// topology key). Nil means the default (true). A deployment
	// with one replica has nothing to spread, so the rendered
	// Deployment omits the rule either way.
	AntiAffinity *bool
	// MaxSkew is the topology-spread maxSkew: the most pods any
	// one node may hold above the least-loaded node. 0 means the
	// default (1, an even spread). A single-replica deployment
	// renders no spread constraint.
	MaxSkew int32
}

// EffectiveMinAvailable resolves MinAvailable against the replica
// count: an explicit value wins, 0 means replicas-1, floored at 0
// (a single replica has nothing to protect).
func (a *AvailabilitySpec) EffectiveMinAvailable(replicas int32) int32 {
	if a != nil && a.MinAvailable != 0 {
		return a.MinAvailable
	}
	if replicas <= 1 {
		return 0
	}
	return replicas - 1
}

// AntiAffinityEnabled reports whether pod anti-affinity applies:
// the default is on, an explicit false turns it off.
func (a *AvailabilitySpec) AntiAffinityEnabled() bool {
	if a == nil || a.AntiAffinity == nil {
		return true
	}
	return *a.AntiAffinity
}

// EffectiveMaxSkew resolves MaxSkew: 0 means the default (1).
func (a *AvailabilitySpec) EffectiveMaxSkew() int32 {
	if a == nil || a.MaxSkew == 0 {
		return 1
	}
	return a.MaxSkew
}

// SecuritySpec configures the pod- and container-level security
// context for a model's serving pods. Every field is optional:
// a nil field takes the operator default, so a manifest that
// says nothing about security still gets the hardened set.
// Pointer booleans (rather than plain bools) are what let the
// operator tell "the author explicitly relaxed this" from "the
// author said nothing" — `mlop validate` warns about the former
// and stays silent about the latter.
type SecuritySpec struct {
	// RunAsNonRoot, when true, makes the kubelet refuse to
	// start the pod if any container would run as uid 0. Nil
	// means the default (true).
	RunAsNonRoot *bool
	// RunAsUser is the uid every container runs as. Nil means
	// the default (10001, a non-root uid with no host meaning).
	// 0 is only legal when RunAsNonRoot is explicitly false:
	// asking for a root container that the kubelet must refuse
	// is a contradiction, and Validate rejects it.
	RunAsUser *int64
	// ReadOnlyRootFilesystem, when true, mounts the container's
	// root filesystem read-only: a compromised model server
	// cannot write its own binary or drop a persistence file.
	// Nil means the default (true). A server that needs a
	// scratch directory should mount an emptyDir volume
	// instead — that is future work, not a reason to relax this.
	ReadOnlyRootFilesystem *bool
	// AllowPrivilegeEscalation, when true, lets a process gain
	// more privileges than its parent (e.g. via a setuid
	// binary). Nil means the default (false).
	AllowPrivilegeEscalation *bool
	// DropCapabilities lists the Linux capabilities removed
	// from the container's bounding set. Nil or empty means the
	// default (["ALL"] — drop everything, add nothing back).
	// An explicit list replaces the default entirely, so a
	// server that needs NET_BIND_SERVICE lists exactly that
	// and nothing else.
	DropCapabilities []string
	// SeccompProfile is the seccomp profile applied to the pod:
	// "RuntimeDefault", "Unconfined", or "Localhost/<path>".
	// Empty means the default ("RuntimeDefault").
	SeccompProfile string
}

// RunAsNonRootEnabled reports whether the pod refuses to run as
// root: the default is on, an explicit false turns it off.
func (s *SecuritySpec) RunAsNonRootEnabled() bool {
	if s == nil || s.RunAsNonRoot == nil {
		return true
	}
	return *s.RunAsNonRoot
}

// EffectiveRunAsUser resolves RunAsUser: nil means the default
// (10001).
func (s *SecuritySpec) EffectiveRunAsUser() int64 {
	if s == nil || s.RunAsUser == nil {
		return DefaultRunAsUser
	}
	return *s.RunAsUser
}

// ReadOnlyRootFilesystemEnabled reports whether the container's
// root filesystem is read-only: the default is on, an explicit
// false turns it off.
func (s *SecuritySpec) ReadOnlyRootFilesystemEnabled() bool {
	if s == nil || s.ReadOnlyRootFilesystem == nil {
		return true
	}
	return *s.ReadOnlyRootFilesystem
}

// AllowPrivilegeEscalationEnabled reports whether a process may
// gain privileges: the default is off, an explicit true turns
// it on.
func (s *SecuritySpec) AllowPrivilegeEscalationEnabled() bool {
	if s == nil || s.AllowPrivilegeEscalation == nil {
		return false
	}
	return *s.AllowPrivilegeEscalation
}

// EffectiveDropCapabilities resolves DropCapabilities: nil or
// empty means the default (drop ALL).
func (s *SecuritySpec) EffectiveDropCapabilities() []string {
	if s == nil || len(s.DropCapabilities) == 0 {
		return []string{"ALL"}
	}
	out := make([]string, len(s.DropCapabilities))
	copy(out, s.DropCapabilities)
	return out
}

// EffectiveSeccompProfile resolves SeccompProfile: empty means
// the default ("RuntimeDefault").
func (s *SecuritySpec) EffectiveSeccompProfile() string {
	if s == nil || s.SeccompProfile == "" {
		return DefaultSeccompProfile
	}
	return s.SeccompProfile
}

// PlacementSpec pins a model's pods to nodes: the node labels
// they require and the node taints they tolerate. Either half
// may be used alone — a nodeSelector without tolerations, or
// tolerations without a selector — and an empty (non-nil) spec
// is the same as no placement at all.
type PlacementSpec struct {
	// NodeSelector requires the pod's node to carry every
	// listed label (e.g. {"accelerator": "nvidia-tesla-t4"}).
	// Empty means no label requirement.
	NodeSelector map[string]string
	// Tolerations lists the node taints the pod tolerates.
	// Empty means the pod tolerates nothing: a tainted node
	// rejects it.
	Tolerations []Toleration
}

// DefaultModelMountPath is where the artifact lands when
// MountPath is unset: a plain, conventional directory the
// serving container can rely on.
const DefaultModelMountPath = "/models"

// DefaultDownloaderImage is the init-container image that
// fetches the artifact. It is deliberately tiny and single
// purpose (curl + sha256sum): the downloader runs before the
// model server starts, so it never shares a process with
// serving traffic.
const DefaultDownloaderImage = "curlimages/curl:8.5.0"

// ModelSourceSpec describes one model artifact to download
// into the pod at startup. The init container fetches URI
// into a shared emptyDir volume mounted at MountPath, and
// the serving container reads the artifact from
// ArtifactPath (mount path + the file name from the URI).
// Only http(s) URIs are supported: artifact stores that need
// credentials (S3, GCS) are future work, and the operator
// says so instead of silently failing the download.
type ModelSourceSpec struct {
	// URI is the http(s) URL of the model artifact, e.g.
	// "https://artifacts.example.com/models/iris-mlp.onnx".
	// Required when the block is present.
	URI string
	// SHA256 is the expected hex digest of the downloaded
	// artifact. Empty means the download is not verified —
	// Lint warns about it, because an unverified artifact
	// is whatever the network handed the pod.
	SHA256 string
	// MountPath is where the shared volume mounts in both
	// the init container and the serving container. Empty
	// means DefaultModelMountPath ("/models").
	MountPath string
	// DownloaderImage is the init-container image. Empty
	// means DefaultDownloaderImage ("curlimages/curl:8.5.0").
	DownloaderImage string
}

// EffectiveMountPath returns the mount path the operator
// renders, resolving the "/models" default for an empty one.
func (m *ModelSourceSpec) EffectiveMountPath() string {
	if m == nil || m.MountPath == "" {
		return DefaultModelMountPath
	}
	return m.MountPath
}

// EffectiveDownloaderImage returns the init-container image
// the operator renders, resolving the curl default for an
// empty one.
func (m *ModelSourceSpec) EffectiveDownloaderImage() string {
	if m == nil || m.DownloaderImage == "" {
		return DefaultDownloaderImage
	}
	return m.DownloaderImage
}

// ArtifactFileName is the file name the artifact lands under:
// the last path segment of the URI, or "model.bin" when the
// URI ends in a slash or has no usable segment. Query strings
// and fragments are stripped first, so a signed URL still
// yields a clean file name.
func (m *ModelSourceSpec) ArtifactFileName() string {
	if m == nil {
		return "model.bin"
	}
	u := m.URI
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	// The file name comes from the path only: strip the scheme
	// and host first, so a bare host ("https://artifacts.example.com")
	// is not mistaken for a file name.
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			u = rest[j:]
		} else {
			return "model.bin"
		}
	}
	// A URI ending in a slash names a directory, not a file:
	// there is no file name to take from it.
	if strings.HasSuffix(u, "/") || u == "" {
		return "model.bin"
	}
	name := u
	if i := strings.LastIndex(u, "/"); i >= 0 {
		name = u[i+1:]
	}
	if name == "" {
		return "model.bin"
	}
	return name
}

// ArtifactPath is the full path the serving container reads
// the artifact from: mount path + file name.
func (m *ModelSourceSpec) ArtifactPath() string {
	if m == nil {
		return ""
	}
	return strings.TrimSuffix(m.EffectiveMountPath(), "/") + "/" + m.ArtifactFileName()
}

// Describe renders the model source as one line for
// `mlop status`, e.g.
// "https://artifacts.example.com/models/iris-mlp.onnx -> /models/iris-mlp.onnx (sha256: 9f2c… verified)".
// A nil source describes as "" (the caller omits the line).
func (m *ModelSourceSpec) Describe() string {
	if m == nil {
		return ""
	}
	verified := "unverified"
	if m.SHA256 != "" {
		verified = "sha256: " + m.SHA256[:12] + "… verified"
	}
	return fmt.Sprintf("%s -> %s (%s)", m.URI, m.ArtifactPath(), verified)
}

// Toleration mirrors the Kubernetes corev1.Toleration, trimmed
// to the fields this operator sets. Operator "" means Equal;
// Effect "" matches a taint of any effect.
type Toleration struct {
	// Key is the taint key to tolerate. Empty with operator
	// Exists matches every taint key.
	Key string
	// Operator is "Equal" (key and value must both match) or
	// "Exists" (the key alone must exist; Value must be
	// empty). "" means "Equal".
	Operator string
	// Value is the taint value to tolerate. Must be empty
	// when Operator is "Exists".
	Value string
	// Effect is the taint effect to tolerate: "NoSchedule",
	// "PreferNoSchedule", "NoExecute", or "" (any effect).
	Effect string
	// TolerationSeconds is how long the pod stays bound after
	// the matching taint appears. Only meaningful with
	// effect NoExecute; nil means evict immediately.
	TolerationSeconds *int64
}

// Describe renders placement as one line for `mlop status`,
// e.g. "nodeSelector accelerator=nvidia-tesla-t4, tolerations
// gpu=true:NoSchedule". A nil or empty placement describes as
// "unconstrained".
func (p *PlacementSpec) Describe() string {
	if p == nil || (len(p.NodeSelector) == 0 && len(p.Tolerations) == 0) {
		return "unconstrained"
	}
	var parts []string
	if len(p.NodeSelector) > 0 {
		keys := make([]string, 0, len(p.NodeSelector))
		for k := range p.NodeSelector {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sel := make([]string, 0, len(keys))
		for _, k := range keys {
			sel = append(sel, k+"="+p.NodeSelector[k])
		}
		parts = append(parts, "nodeSelector "+strings.Join(sel, ","))
	}
	if len(p.Tolerations) > 0 {
		tols := make([]string, 0, len(p.Tolerations))
		for _, t := range p.Tolerations {
			tols = append(tols, t.String())
		}
		parts = append(parts, "tolerations "+strings.Join(tols, ", "))
	}
	return strings.Join(parts, ", ")
}

// String renders one toleration in the compact form the YAML
// manifest dialect uses: "key=value:Effect",
// "key:Exists:Effect", "key=value:NoExecute:300". The effect
// is omitted when empty, the seconds when unset.
func (t Toleration) String() string {
	var b strings.Builder
	b.WriteString(t.Key)
	if t.Operator == "Exists" {
		b.WriteString(":Exists")
	} else if t.Value != "" {
		b.WriteString("=")
		b.WriteString(t.Value)
	}
	if t.Effect != "" {
		b.WriteString(":")
		b.WriteString(t.Effect)
	}
	if t.TolerationSeconds != nil {
		b.WriteString(":")
		b.WriteString(strconv.FormatInt(*t.TolerationSeconds, 10))
	}
	return b.String()
}

// AutoscalingSpec configures horizontal autoscaling for the model.
type AutoscalingSpec struct {
	// MinReplicas is the floor; 0 means unset (defaults to 1).
	MinReplicas int32
	// MaxReplicas is the ceiling; must be >= MinReplicas.
	MaxReplicas int32
	// TargetCPUUtilization is the average CPU percentage that triggers
	// scaling (1-100). 0 disables CPU-based scaling.
	TargetCPUUtilization int32
	// TargetRequestsPerSecond is the per-pod request rate that triggers
	// scaling. 0 disables RPS-based scaling.
	TargetRequestsPerSecond int32
}

// HealthSpec configures the readiness gate for the model container.
type HealthSpec struct {
	// Path is the HTTP readiness endpoint, e.g. "/healthz".
	Path string
	// InitialDelaySeconds is the wait after container start before the
	// first probe.
	InitialDelaySeconds int32
	// PeriodSeconds is the probe interval.
	PeriodSeconds int32
}

// ModelDeploymentStatus is the observed state of a deployed model.
type ModelDeploymentStatus struct {
	// Phase is the current lifecycle phase.
	Phase Phase
	// ReadyReplicas is the number of pods passing the readiness gate.
	ReadyReplicas int32
	// ObservedGeneration is the last spec generation the operator acted on.
	ObservedGeneration int64
	// Message is a human-readable summary of the current state.
	Message string
	// PreviousImage is the container image the stable Deployment
	// served before the current one (e.g. the v1 image after a
	// v2 promotion). Empty until the first image change. `mlop
	// rollback` restores it. The reconciler updates it whenever
	// the stable image actually changes — on a canary promotion
	// and on a direct (rolling/recreate) image change — and
	// preserves it across reconciles that change nothing.
	PreviousImage string
	// PreviousVersion is the model version label that went with
	// PreviousImage.
	PreviousVersion string
	// Conditions are the latest condition per type (Ready, Progressing,
	// Available), newest first within a type is not guaranteed — use
	// ConditionFor to look one up.
	Conditions []Condition
}

// Condition is a single status condition, following the Kubernetes
// condition conventions (Type/Status/Reason/Message). LastTransitionTime
// is the last time the Status value changed: an operator (or a human
// reading `mlop status`) can tell how long a deployment has been
// Degraded, or when it last became Available, without digging through
// the event log. It is stamped by SetConditionAt, never set by hand.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"` // "True", "False", "Unknown"
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// ConditionFor returns the condition of the given type, or nil.
func (s *ModelDeploymentStatus) ConditionFor(condType string) *Condition {
	for i := range s.Conditions {
		if s.Conditions[i].Type == condType {
			return &s.Conditions[i]
		}
	}
	return nil
}

// SetCondition upserts a condition of the given type, stamping the
// transition time with the wall clock. See SetConditionAt for the
// semantics.
func (s *ModelDeploymentStatus) SetCondition(c Condition) {
	s.SetConditionAt(c, time.Now())
}

// SetConditionAt upserts a condition of the given type, following the
// Kubernetes transition-time rules:
//
//   - a brand-new condition type is stamped with now;
//   - an update that keeps the same Status keeps the old
//     LastTransitionTime (only Reason/Message change — the condition
//     did not transition);
//   - a Status flip is stamped with now.
//
// A stored condition with a zero LastTransitionTime (e.g. loaded from
// a state file written before transition times existed) is stamped
// with now on first sight: the operator just observed it in this
// state, and the clock is the source of truth from here on.
func (s *ModelDeploymentStatus) SetConditionAt(c Condition, now time.Time) {
	for i := range s.Conditions {
		if s.Conditions[i].Type == c.Type {
			old := s.Conditions[i]
			if old.Status == c.Status && !old.LastTransitionTime.IsZero() {
				c.LastTransitionTime = old.LastTransitionTime
			} else {
				c.LastTransitionTime = now
			}
			s.Conditions[i] = c
			return
		}
	}
	c.LastTransitionTime = now
	s.Conditions = append(s.Conditions, c)
}

// Key returns the registry key for this deployment ("namespace/name").
func (d *ModelDeployment) Key() string {
	ns := d.Namespace
	if ns == "" {
		ns = "default"
	}
	return ns + "/" + d.Name
}
