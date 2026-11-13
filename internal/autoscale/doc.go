// Package autoscale computes horizontal scaling decisions for a
// ModelDeployment: how many replicas the serving Deployment should
// run, given the observed load. It is pure decision logic — no
// cluster calls — so the reconciler and the tests drive it the same
// way. The algorithm follows the Kubernetes HPA shape: each enabled
// target (average CPU utilization, per-pod request rate) proposes a
// replica count, the largest proposal wins, and the result is
// clamped to the spec's [minReplicas, maxReplicas].
//
// Scale-up applies immediately; scale-down waits out a
// stabilization window (default 5 minutes, the HPA default), so a
// brief dip in load does not churn pods. A Scaler holds that
// per-deployment state; DesiredReplicas is the stateless core for
// callers that do not need stabilization.
//
// While autoscaling is on, the scaler owns the replica count:
// spec.replicas is only the starting point, and later edits to it
// do not move the deployment — the same ownership the Kubernetes
// HPA has over the Deployment it scales.
package autoscale
