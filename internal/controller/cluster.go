package controller

import (
	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// ClusterMetrics is one observation of a deployment's live load and
// health, as reported by the cluster (metrics server for CPU/RPS,
// the model server for error rate, latency and the model-load
// probe). A value of -1 (or 0 for latency) means "no observation":
// the consumer skips that signal instead of treating it as zero.
type ClusterMetrics struct {
	// CPUUtilizationPercent is the average CPU utilization across
	// pods (percent of requested CPU). -1 means unknown.
	CPUUtilizationPercent float64
	// RequestsPerSecond is the average request rate per pod. -1
	// means unknown.
	RequestsPerSecond float64
	// ErrorRate is the fraction of failed requests (0-1). -1 means
	// unknown.
	ErrorRate float64
	// LatencyP95Ms is the p95 request latency in milliseconds. 0
	// means unknown.
	LatencyP95Ms float64
	// ModelLoadKnown is true when the model-load probe reported.
	ModelLoadKnown bool
	// ModelLoaded is true when the model finished loading into the
	// serving container.
	ModelLoaded bool
	// ModelLoadFailed is true when the probe reported that the model
	// failed to load. Only meaningful when ModelLoadKnown is true.
	ModelLoadFailed bool
}

// Cluster is the minimal cluster surface the reconciler needs. It is
// deliberately narrow — get/apply/delete for Deployments, Services,
// HPAs and PDBs plus readiness and load metrics — so the real
// Kubernetes client and the in-memory fake used by tests and the
// demo share one contract. Keys are "namespace/name" (see
// api.ModelDeployment.Key); canary Deployments use the key
// "namespace/name-canary".
type Cluster interface {
	// GetDeployment returns the live Deployment for key, or false
	// when the cluster has none.
	GetDeployment(key string) (render.Deployment, bool)
	// ApplyDeployment creates or updates the Deployment. The call is
	// idempotent: applying the same Deployment twice is a no-op.
	ApplyDeployment(d render.Deployment) error
	// DeleteDeployment removes the Deployment; deleting a missing one
	// is not an error.
	DeleteDeployment(key string) error
	// GetService returns the live Service for key, or false.
	GetService(key string) (render.Service, bool)
	// ApplyService creates or updates the Service.
	ApplyService(s render.Service) error
	// DeleteService removes the Service; deleting a missing one is
	// not an error.
	DeleteService(key string) error
	// GetHPA returns the live HorizontalPodAutoscaler for key, or
	// false when the cluster has none.
	GetHPA(key string) (render.HPA, bool)
	// ApplyHPA creates or updates the HPA.
	ApplyHPA(h render.HPA) error
	// DeleteHPA removes the HPA; deleting a missing one is not an
	// error.
	DeleteHPA(key string) error
	// GetPDB returns the live PodDisruptionBudget for key, or
	// false when the cluster has none.
	GetPDB(key string) (render.PodDisruptionBudget, bool)
	// ApplyPDB creates or updates the PodDisruptionBudget.
	ApplyPDB(p render.PodDisruptionBudget) error
	// DeletePDB removes the PodDisruptionBudget; deleting a
	// missing one is not an error.
	DeletePDB(key string) error
	// ReadyReplicas reports how many pods of the Deployment at key
	// currently pass the readiness gate (0 for a missing Deployment).
	ReadyReplicas(key string) int32
	// Metrics returns the latest load/health observation for key,
	// or false when the cluster has none (no metrics server, or
	// the deployment is too new to have samples).
	Metrics(key string) (ClusterMetrics, bool)
}
