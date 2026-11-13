// Package rollout owns the canary rollout state machine: the
// traffic-weight ladder, the health gate, and the step/promote/abort
// decisions. It is pure Go — no cluster calls — so every decision is
// unit-testable without a fake cluster. The controller owns the
// per-deployment state and calls into this package once per reconcile.
//
// The traffic model is pod-count based, the way a plain Kubernetes
// Service splits traffic: the Service selector matches both the
// stable and the canary pods, so the share of traffic reaching the
// canary is roughly canary-ready-pods / total-ready-pods. The weight
// ladder therefore sizes the canary Deployment (see CanaryReplicas),
// and promotion folds the new image into the stable Deployment and
// deletes the canary.
package rollout
