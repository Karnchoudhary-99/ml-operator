package controller

import (
	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// Cluster is the minimal cluster surface the reconciler needs. It is
// deliberately narrow — get/apply/delete for Deployments and Services
// plus readiness — so the real Kubernetes client and the in-memory
// fake used by tests and the demo share one contract. Keys are
// "namespace/name" (see api.ModelDeployment.Key); canary Deployments
// use the key "namespace/name-canary".
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
	// ReadyReplicas reports how many pods of the Deployment at key
	// currently pass the readiness gate (0 for a missing Deployment).
	ReadyReplicas(key string) int32
}
