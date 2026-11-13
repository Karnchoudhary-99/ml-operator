package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/render"
	"github.com/Karnchoudhary-99/ml-operator/internal/rollout"
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
//
// For Canary rollouts the reconciler also drives the rollout state
// machine (internal/rollout): the stable Deployment keeps serving
// the previous version while the canary Deployment steps up its
// traffic weight behind the health gate, and promotion folds the new
// image into the stable Deployment and deletes the canary.
type Reconciler struct {
	Registry *registry.Registry
	Cluster  Cluster
	// Now is the clock the rollout state machine reads. Tests set it
	// to a controllable clock; nil means time.Now.
	Now func() time.Time

	// rollouts is the per-deployment canary rollout state, keyed by
	// "namespace/name". A done state (promoted or aborted) stays in
	// the map so a later reconcile of the same generation does not
	// restart the rollout; a new generation resets it.
	rollouts map[string]rollout.State
}

// New returns a Reconciler over reg and cluster.
func New(reg *registry.Registry, cluster Cluster) *Reconciler {
	return &Reconciler{Registry: reg, Cluster: cluster, rollouts: make(map[string]rollout.State)}
}

// now returns the reconciler's clock.
func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
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

	// Canary rollouts keep their state across reconciles; any other
	// strategy drops it, so returning to Canary later starts fresh.
	liveStable, liveOK := r.Cluster.GetDeployment(key)
	var rs rollout.State
	var hasRollout bool
	if d.Spec.Strategy == api.StrategyCanary && d.Spec.Canary != nil {
		rs, hasRollout = r.rolloutState(key, d), true
	} else {
		delete(r.rollouts, key)
	}

	desired := render.DeploymentFor(d)
	var canary *render.Deployment
	if hasRollout {
		if liveOK && !(rs.Done && !rs.Aborted) {
			// The rollout is in flight, or it aborted: the stable
			// Deployment keeps serving the previous version until
			// a promotion. Only a promoted rollout unpins it.
			// Only the image and version label are pinned — other
			// spec changes (port, resources, env) still apply.
			desired.Container.Image = liveStable.Container.Image
			desired.Labels["ml-operator/version"] = liveStable.Labels["ml-operator/version"]
		}
		if !rs.Done {
			c := render.CanaryDeploymentForWeight(d, rs.CurrentWeight())
			canary = &c
		}
	}
	desiredSvc := render.ServiceFor(d)

	plan := r.plan(key, d, desired, desiredSvc, canary)
	actions := plan
	for _, a := range plan {
		if err := r.apply(a, desired, canary, desiredSvc); err != nil {
			// A failed apply is Degraded, not Failed: the spec is
			// fine, the cluster operation is what broke, and the
			// caller should retry.
			msg := fmt.Sprintf("apply %s %s failed: %v", a.Kind, a.Key, err)
			r.updateStatus(key, d, api.PhaseDegraded, r.Cluster.ReadyReplicas(key), msg)
			return Result{Actions: actions, Phase: api.PhaseDegraded, Requeue: true, Message: msg}, err
		}
	}

	// A canary rollout in flight is judged after the apply: the gate
	// reads the live canary's readiness, and the state machine
	// decides hold / advance / promote / abort.
	if hasRollout && !rs.Done {
		return r.reconcileCanary(key, d, rs, actions)
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
	return Result{Actions: actions, Phase: phase, Requeue: phase != api.PhaseAvailable, Message: msg}, nil
}

// rolloutState returns the rollout state for key, starting a fresh
// one when there is none, the generation changed, or the canary spec
// (and therefore the weight ladder) changed. A done state is kept:
// it records that this generation already promoted or aborted, so
// reconciling the same generation again never restarts the rollout.
func (r *Reconciler) rolloutState(key string, d api.ModelDeployment) rollout.State {
	ladder := rollout.StepWeights(*d.Spec.Canary)
	if s, ok := r.rollouts[key]; ok && s.Generation == d.Generation && equalWeights(s.Weights, ladder) {
		return s
	}
	s := rollout.New(d.Generation, *d.Spec.Canary, r.now())
	r.rollouts[key] = s
	return s
}

// reconcileCanary evaluates the health gate for an in-flight canary
// rollout and acts on the state machine's decision. It returns the
// reconcile result; the caller has already applied the plan.
func (r *Reconciler) reconcileCanary(key string, d api.ModelDeployment, rs rollout.State, actions []Action) (Result, error) {
	ns, name, _ := strings.Cut(key, "/")
	canaryKey := ns + "/" + name + "-canary"
	interval := time.Duration(d.Spec.Canary.IntervalSeconds) * time.Second

	// The gate is readiness-only for now: the cluster reports ready
	// replicas, and there is no metrics source yet for error rate or
	// latency. The metrics phase fills those in.
	var ev rollout.Evaluation
	if liveCanary, ok := r.Cluster.GetDeployment(canaryKey); ok {
		ev = rollout.Evaluate(rollout.Gate{}, rollout.Sample{
			ReadyReplicas:   r.Cluster.ReadyReplicas(canaryKey),
			DesiredReplicas: liveCanary.Replicas,
		})
	} else {
		ev = rollout.Evaluation{Pending: true, Reasons: []string{"canary Deployment not created yet"}}
	}

	oldWeight := rs.CurrentWeight()
	next, decision := rollout.Next(rs, ev, r.now(), interval, 0)
	r.rollouts[key] = next

	stableReady := r.Cluster.ReadyReplicas(key)
	desiredReplicas := int32(1)
	if d.Spec.Replicas != nil {
		desiredReplicas = *d.Spec.Replicas
	}

	switch decision {
	case rollout.DecisionAbort:
		// The canary failed the gate too many times: remove it and
		// report Degraded. The stable Deployment keeps serving the
		// previous version. Terminal for this generation.
		actions = withoutNoOp(actions)
		if err := r.Cluster.DeleteDeployment(canaryKey); err != nil {
			return Result{Actions: actions}, err
		}
		actions = append(actions, Action{Kind: ActionDeleteDeployment, Key: canaryKey,
			Reason: fmt.Sprintf("canary aborted after %d failed health gates", next.Failures)})
		msg := fmt.Sprintf("canary aborted after %d failed health gates: %s", next.Failures, strings.Join(ev.Reasons, "; "))
		r.updateStatus(key, d, api.PhaseDegraded, stableReady, msg)
		return Result{Actions: actions, Phase: api.PhaseDegraded, Message: msg}, nil

	case rollout.DecisionPromote:
		// The gate passed at the final weight: the stable
		// Deployment takes the new image and the canary goes away.
		actions = withoutNoOp(actions)
		promoted := render.DeploymentFor(d)
		if err := r.Cluster.ApplyDeployment(promoted); err != nil {
			msg := fmt.Sprintf("promote: apply stable Deployment failed: %v", err)
			r.updateStatus(key, d, api.PhaseDegraded, stableReady, msg)
			return Result{Actions: actions, Phase: api.PhaseDegraded, Requeue: true, Message: msg}, err
		}
		actions = append(actions, Action{Kind: ActionUpdateDeployment, Key: key,
			Reason: fmt.Sprintf("canary promoted: stable now serves %q", d.Spec.Model.Image)})
		if err := r.Cluster.DeleteDeployment(canaryKey); err != nil {
			return Result{Actions: actions}, err
		}
		actions = append(actions, Action{Kind: ActionDeleteDeployment, Key: canaryKey, Reason: "canary promoted"})
		phase := api.PhaseProgressing
		msg := fmt.Sprintf("canary promoted at 100%% — stable now serves %s (%d/%d replicas ready)",
			d.Spec.Model.Version, stableReady, desiredReplicas)
		if stableReady >= desiredReplicas {
			phase = api.PhaseAvailable
		} else if stableReady > 0 {
			phase = api.PhaseDegraded
		}
		r.updateStatus(key, d, phase, stableReady, msg)
		return Result{Actions: actions, Phase: phase, Requeue: phase != api.PhaseAvailable, Message: msg}, nil

	case rollout.DecisionAdvance:
		// Step the canary to the next weight now, so the new size
		// is live for the next reconcile's gate evaluation.
		actions = withoutNoOp(actions)
		c := render.CanaryDeploymentForWeight(d, next.CurrentWeight())
		if err := r.Cluster.ApplyDeployment(c); err != nil {
			msg := fmt.Sprintf("canary weight step failed: %v", err)
			r.updateStatus(key, d, api.PhaseDegraded, stableReady, msg)
			return Result{Actions: actions, Phase: api.PhaseDegraded, Requeue: true, Message: msg}, err
		}
		actions = append(actions, Action{Kind: ActionUpdateDeployment, Key: canaryKey,
			Reason: fmt.Sprintf("canary weight %d%% -> %d%%", oldWeight, next.CurrentWeight())})
	}

	// Hold or advance: the rollout is still in flight. The phase is
	// Progressing while the stable Deployment serves, Degraded only
	// when it is below its replica count. Either way the caller must
	// reconcile again — the rollout only finishes on promote/abort.
	phase := api.PhaseProgressing
	if stableReady < desiredReplicas && stableReady > 0 {
		phase = api.PhaseDegraded
	}
	gateSummary := "gate passed"
	if !ev.Pass {
		gateSummary = "gate: " + strings.Join(ev.Reasons, "; ")
	}
	msg := fmt.Sprintf("canary %d%% (step %d/%d) — %s", next.CurrentWeight(), next.Step+1, len(next.Weights), gateSummary)
	if decision == rollout.DecisionAdvance {
		msg = fmt.Sprintf("canary advanced to %d%% (step %d/%d)", next.CurrentWeight(), next.Step+1, len(next.Weights))
	}
	r.updateStatus(key, d, phase, stableReady, msg)
	return Result{Actions: actions, Phase: phase, Requeue: true, Message: msg}, nil
}

// plan diffs the desired state against the live cluster and returns
// the minimal ordered apply plan: Deployment first, then canary (if
// any), then Service. It never mutates the cluster. canary is nil
// when no canary Deployment is desired for this reconcile — either
// the strategy is not Canary, or the rollout already finished.
func (r *Reconciler) plan(key string, d api.ModelDeployment, desired render.Deployment, desiredSvc render.Service, canary *render.Deployment) []Action {
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

	ns, name, _ := strings.Cut(key, "/")
	canaryKey := ns + "/" + name + "-canary"
	if canary != nil {
		liveCanary, ok := r.Cluster.GetDeployment(canaryKey)
		if !ok {
			actions = append(actions, Action{Kind: ActionCreateDeployment, Key: canaryKey, Reason: "canary deployment does not exist"})
		} else if reasons := render.DeploymentDiffReasons(*canary, liveCanary); len(reasons) > 0 {
			actions = append(actions, Action{Kind: ActionUpdateDeployment, Key: canaryKey, Reason: strings.Join(reasons, ", ")})
		}
	} else if _, ok := r.Cluster.GetDeployment(canaryKey); ok {
		// No canary is desired: the strategy moved away from Canary,
		// or the rollout finished. Remove the leftover canary
		// Deployment so it stops serving.
		actions = append(actions, Action{Kind: ActionDeleteDeployment, Key: canaryKey, Reason: "no canary deployment desired"})
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

// apply executes one plan action against the cluster, resolving the
// object from the desired state the plan was built from.
func (r *Reconciler) apply(a Action, desired render.Deployment, canary *render.Deployment, svc render.Service) error {
	switch a.Kind {
	case ActionCreateDeployment, ActionUpdateDeployment:
		stableKey := desired.Namespace + "/" + desired.Name
		if a.Key == stableKey {
			return r.Cluster.ApplyDeployment(desired)
		}
		if canary != nil {
			canaryKey := canary.Namespace + "/" + canary.Name
			if a.Key == canaryKey {
				return r.Cluster.ApplyDeployment(*canary)
			}
		}
		return fmt.Errorf("no desired deployment for key %q", a.Key)
	case ActionDeleteDeployment:
		return r.Cluster.DeleteDeployment(a.Key)
	case ActionCreateService, ActionUpdateService:
		return r.Cluster.ApplyService(svc)
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
	delete(r.rollouts, key)
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

func equalWeights(a, b []int32) bool {
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

// withoutNoOp drops a lone NoOp action: the plan found nothing to
// do, but the rollout decision (advance/promote/abort) does real
// work, so the result should list that work instead of a NoOp.
func withoutNoOp(actions []Action) []Action {
	if len(actions) == 1 && actions[0].Kind == ActionNoOp {
		return actions[:0]
	}
	return actions
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
