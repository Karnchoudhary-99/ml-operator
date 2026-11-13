package controller

import (
	"fmt"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// ActionKind classifies one step of an apply plan.
type ActionKind string

const (
	// ActionCreateDeployment creates the serving Deployment.
	ActionCreateDeployment ActionKind = "CreateDeployment"
	// ActionUpdateDeployment updates the serving Deployment in place.
	ActionUpdateDeployment ActionKind = "UpdateDeployment"
	// ActionDeleteDeployment deletes a Deployment (recreate rollouts
	// and garbage collection).
	ActionDeleteDeployment ActionKind = "DeleteDeployment"
	// ActionCreateService creates the fronting Service.
	ActionCreateService ActionKind = "CreateService"
	// ActionUpdateService updates the fronting Service.
	ActionUpdateService ActionKind = "UpdateService"
	// ActionDeleteService deletes the fronting Service.
	ActionDeleteService ActionKind = "DeleteService"
	// ActionNoOp means the live state already matches the desired
	// state; the plan contains exactly one NoOp action.
	ActionNoOp ActionKind = "NoOp"
)

// Action is one step of an apply plan.
type Action struct {
	Kind ActionKind
	// Key is the "namespace/name" of the object acted on.
	Key string
	// Reason is a human-readable why, e.g. `image "v1" -> "v2"`.
	Reason string
}

// Result is the outcome of one Reconcile call.
type Result struct {
	Actions []Action
	// Phase is the phase recorded in the registry status after this
	// reconcile. It is empty when the key is gone from the registry
	// (the deployment was deleted, so there is no status to report).
	Phase api.Phase
	// Requeue asks the caller to reconcile this key again: the
	// deployment is not yet Available and progress depends on the
	// cluster catching up (pods becoming ready).
	Requeue bool
	Message string
}

// Reconciler drives one ModelDeployment toward its desired state:
// render, diff against the live cluster, apply only what changed,
// then record the phase in the registry status. It is
// level-triggered and idempotent — reconciling an in-sync deployment
// applies nothing.
type Reconciler struct {
	Registry *registry.Registry
	Cluster  Cluster
}

// New returns a Reconciler over reg and cluster.
func New(reg *registry.Registry, cluster Cluster) *Reconciler {
	return &Reconciler{Registry: reg, Cluster: cluster}
}

// Reconcile converges the deployment at key ("namespace/name").
func (r *Reconciler) Reconcile(key string) (Result, error) {
	entry := r.Registry.Get(key)
	if entry == nil {
		// The deployment left the registry: garbage-collect any
		// cluster objects it owned.
		return r.reconcileDeleted(key)
	}

	d := entry.Deployment
	api.ApplyDefaults(&d)
	if errs := api.Validate(&d); len(errs) > 0 {
		// An invalid spec never reaches the cluster. Failed is
		// terminal for this generation: no requeue, the next spec
		// change (new generation) triggers a fresh reconcile.
		msg := "invalid spec: " + strings.Join(errorStrings(errs), "; ")
		r.updateStatus(key, d, api.PhaseFailed, 0, msg)
		return Result{Phase: api.PhaseFailed, Message: msg}, nil
	}

	desired := render.DeploymentFor(d)
	desiredSvc := render.ServiceFor(d)
	plan := r.plan(key, d, desired, desiredSvc)
	for _, a := range plan {
		if err := r.apply(a, d); err != nil {
			// A failed apply is Degraded, not Failed: the spec is
			// fine, the cluster operation is what broke, and the
			// caller should retry.
			msg := fmt.Sprintf("apply %s %s failed: %v", a.Kind, a.Key, err)
			r.updateStatus(key, d, api.PhaseDegraded, r.Cluster.ReadyReplicas(key), msg)
			return Result{Actions: plan, Phase: api.PhaseDegraded, Requeue: true, Message: msg}, err
		}
	}

	ready := r.Cluster.ReadyReplicas(key)
	phase := api.PhaseProgressing
	msg := fmt.Sprintf("%d/%d replicas ready", ready, desired.Replicas)
	switch {
	case ready >= desired.Replicas:
		phase = api.PhaseAvailable
	case ready > 0:
		// Serving, but below the desired replica count.
		phase = api.PhaseDegraded
	}
	r.updateStatus(key, d, phase, ready, msg)
	return Result{Actions: plan, Phase: phase, Requeue: phase != api.PhaseAvailable, Message: msg}, nil
}

// plan diffs the desired state against the live cluster and returns
// the minimal ordered apply plan: Deployment first, then canary (if
// any), then Service. It never mutates the cluster.
func (r *Reconciler) plan(key string, d api.ModelDeployment, desired render.Deployment, desiredSvc render.Service) []Action {
	var actions []Action

	live, ok := r.Cluster.GetDeployment(key)
	if !ok {
		actions = append(actions, Action{Kind: ActionCreateDeployment, Key: key, Reason: "deployment does not exist"})
	} else if reasons := render.DeploymentDiffReasons(desired, live); len(reasons) > 0 {
		if d.Spec.Strategy == api.StrategyRecreate && desired.Container.Image != live.Container.Image {
			// Recreate: tear the old pods down before starting new
			// ones, so the old and new model versions never serve
			// side by side.
			actions = append(actions,
				Action{Kind: ActionDeleteDeployment, Key: key, Reason: "recreate: image " + quotePair(live.Container.Image, desired.Container.Image)},
				Action{Kind: ActionCreateDeployment, Key: key, Reason: "recreate: image " + quotePair(live.Container.Image, desired.Container.Image)},
			)
		} else {
			actions = append(actions, Action{Kind: ActionUpdateDeployment, Key: key, Reason: strings.Join(reasons, ", ")})
		}
	}

	canary := render.CanaryDeploymentFor(d)
	canaryKey := canary.Namespace + "/" + canary.Name
	if d.Spec.Strategy == api.StrategyCanary {
		liveCanary, ok := r.Cluster.GetDeployment(canaryKey)
		if !ok {
			actions = append(actions, Action{Kind: ActionCreateDeployment, Key: canaryKey, Reason: "canary deployment does not exist"})
		} else if reasons := render.DeploymentDiffReasons(canary, liveCanary); len(reasons) > 0 {
			actions = append(actions, Action{Kind: ActionUpdateDeployment, Key: canaryKey, Reason: strings.Join(reasons, ", ")})
		}
	} else if _, ok := r.Cluster.GetDeployment(canaryKey); ok {
		// The strategy moved away from Canary: remove the leftover
		// canary Deployment so it stops serving.
		actions = append(actions, Action{Kind: ActionDeleteDeployment, Key: canaryKey, Reason: "strategy is no longer Canary"})
	}

	liveSvc, ok := r.Cluster.GetService(key)
	if !ok {
		actions = append(actions, Action{Kind: ActionCreateService, Key: key, Reason: "service does not exist"})
	} else if reasons := render.ServiceDiffReasons(desiredSvc, liveSvc); len(reasons) > 0 {
		actions = append(actions, Action{Kind: ActionUpdateService, Key: key, Reason: strings.Join(reasons, ", ")})
	}

	if len(actions) == 0 {
		return []Action{{Kind: ActionNoOp, Key: key, Reason: "live state matches desired state"}}
	}
	return actions
}

// apply executes one plan action against the cluster.
func (r *Reconciler) apply(a Action, d api.ModelDeployment) error {
	switch a.Kind {
	case ActionCreateDeployment, ActionUpdateDeployment:
		return r.Cluster.ApplyDeployment(desiredDeploymentFor(d, a.Key))
	case ActionDeleteDeployment:
		return r.Cluster.DeleteDeployment(a.Key)
	case ActionCreateService, ActionUpdateService:
		return r.Cluster.ApplyService(render.ServiceFor(d))
	case ActionDeleteService:
		return r.Cluster.DeleteService(a.Key)
	case ActionNoOp:
		return nil
	}
	return fmt.Errorf("unknown action %q", a.Kind)
}

// reconcileDeleted removes the cluster objects owned by a deployment
// that left the registry: stable Deployment, canary Deployment (if
// any), and Service. Deleting something already gone is a no-op.
func (r *Reconciler) reconcileDeleted(key string) (Result, error) {
	var actions []Action
	ns, name, _ := strings.Cut(key, "/")
	canaryKey := ns + "/" + name + "-canary"

	for _, target := range []struct {
		kind ActionKind
		key  string
		get  func(string) bool
		del  func(string) error
	}{
		{ActionDeleteDeployment, key, r.hasDeployment, r.Cluster.DeleteDeployment},
		{ActionDeleteDeployment, canaryKey, r.hasDeployment, r.Cluster.DeleteDeployment},
		{ActionDeleteService, key, r.hasService, r.Cluster.DeleteService},
	} {
		if !target.get(target.key) {
			continue
		}
		if err := target.del(target.key); err != nil {
			return Result{Actions: actions}, err
		}
		actions = append(actions, Action{Kind: target.kind, Key: target.key, Reason: "deployment removed from registry"})
	}

	if len(actions) == 0 {
		return Result{Actions: []Action{{Kind: ActionNoOp, Key: key, Reason: "nothing to clean up"}}, Message: "nothing to clean up"}, nil
	}
	return Result{Actions: actions, Message: "cleaned up cluster objects"}, nil
}

func (r *Reconciler) hasDeployment(key string) bool {
	_, ok := r.Cluster.GetDeployment(key)
	return ok
}

func (r *Reconciler) hasService(key string) bool {
	_, ok := r.Cluster.GetService(key)
	return ok
}

// updateStatus records the reconcile outcome in the registry. The
// Ready / Progressing / Available conditions are rebuilt from the
// phase every time, so a stale condition can never linger.
func (r *Reconciler) updateStatus(key string, d api.ModelDeployment, phase api.Phase, ready int32, msg string) {
	status := api.ModelDeploymentStatus{
		Phase:              phase,
		ReadyReplicas:      ready,
		ObservedGeneration: d.Generation,
		Message:            msg,
	}
	status.SetCondition(api.Condition{Type: "Ready", Status: boolStatus(phase == api.PhaseAvailable), Reason: string(phase), Message: msg})
	status.SetCondition(api.Condition{Type: "Progressing", Status: boolStatus(phase == api.PhaseProgressing), Reason: string(phase), Message: msg})
	status.SetCondition(api.Condition{Type: "Available", Status: boolStatus(phase == api.PhaseAvailable), Reason: string(phase), Message: msg})
	r.Registry.UpdateStatus(key, status)
}

// desiredDeploymentFor resolves which rendered Deployment an action
// key refers to: the stable one, or the canary one.
func desiredDeploymentFor(d api.ModelDeployment, key string) render.Deployment {
	stable := render.DeploymentFor(d)
	if stable.Namespace+"/"+stable.Name == key {
		return stable
	}
	return render.CanaryDeploymentFor(d)
}

func boolStatus(v bool) string {
	if v {
		return "True"
	}
	return "False"
}

func quotePair(old, new string) string {
	return fmt.Sprintf("%q -> %q", old, new)
}

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
