// Drift repair: close the loop drift detection opened. `mlop drift`
// tells the human what moved out of band; repair puts the desired
// state back, object by object, using the same Plan the reconciler
// uses — so repair and reconcile can never disagree about what
// "correct" looks like.
//
// Safety rules, deliberately conservative:
//
//   - Repair only restores what the spec asks for: drifted and
//     missing objects are re-applied; in-sync objects are untouched.
//   - Unexpected objects are NEVER deleted. A leftover canary, an
//     HPA after autoscaling was turned off, a green Deployment after
//     a strategy change — each might be serving live traffic, or
//     might be evidence the manifest is stale and the live change
//     was the intended one. Repair reports them as left as-is and
//     leaves the deletion to the human (or to a reconcile, which
//     owns that decision).
//   - A paused deployment is refused outright: nothing is repaired
//     until resume, because the pause is the operator's promise
//     that live objects keep serving exactly as they are.
//   - Repair never advances a rollout and never changes a phase:
//     it fixes objects, not state machines. A canary in flight
//     keeps its pinned stable image, exactly as drift reports it.
//
// A dry run computes the same drift + plan and reports what would
// change, applying nothing.
package controller

import (
	"fmt"
	"sort"

	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

// RepairOutcome is what repair did (or would do, on a dry run) to
// one managed object.
type RepairOutcome string

const (
	// RepairFixed means the object was re-applied to the desired
	// state and a fresh drift check confirms it in sync.
	RepairFixed RepairOutcome = "repaired"
	// RepairWouldFix is the dry-run twin of RepairFixed: the
	// object would be re-applied, nothing was written.
	RepairWouldFix RepairOutcome = "would-repair"
	// RepairLeft means the object was deliberately not touched:
	// an unexpected object repair never deletes, or a paused
	// deployment's objects, which repair refuses to fix.
	RepairLeft RepairOutcome = "left"
	// RepairFailed means the cluster write errored (after the
	// retry budget). Other objects are still attempted.
	RepairFailed RepairOutcome = "failed"
	// RepairUnchanged means the object was already in sync.
	RepairUnchanged RepairOutcome = "unchanged"
)

// ResourceRepair is the repair verdict for one managed object.
type ResourceRepair struct {
	// Kind is Deployment, Service, HPA, or PodDisruptionBudget.
	Kind string `json:"kind"`
	// Key is the "namespace/name" of the object.
	Key string `json:"key"`
	// Was is the drift status before repair.
	Was DriftStatus `json:"was"`
	// Outcome is what repair did to it.
	Outcome RepairOutcome `json:"outcome"`
	// Detail names the change (the plan reason for a fix), the
	// reason it was left alone, or the apply error.
	Detail string `json:"detail,omitempty"`
}

// DeploymentRepair is the repair report for one deployment.
type DeploymentRepair struct {
	Deployment string           `json:"deployment"`
	Paused     bool             `json:"paused"`
	DryRun     bool             `json:"dryRun"`
	Results    []ResourceRepair `json:"results"`
}

// Fixed counts objects restored to the desired state (or that
// would be, on a dry run).
func (rep DeploymentRepair) Fixed() int {
	n := 0
	for _, res := range rep.Results {
		if res.Outcome == RepairFixed || res.Outcome == RepairWouldFix {
			n++
		}
	}
	return n
}

// Left counts objects repair deliberately did not touch.
func (rep DeploymentRepair) Left() int {
	n := 0
	for _, res := range rep.Results {
		if res.Outcome == RepairLeft {
			n++
		}
	}
	return n
}

// Failed counts objects whose cluster write errored.
func (rep DeploymentRepair) Failed() int {
	n := 0
	for _, res := range rep.Results {
		if res.Outcome == RepairFailed {
			n++
		}
	}
	return n
}

// AllFixed is true when nothing is left unrepaired and nothing
// failed: every drifted or missing object is back in sync and no
// unexpected object is still standing. On a dry run it is false
// whenever something would have been repaired, so the command
// exits 1 and works as a CI gate — the same as `mlop drift`.
func (rep DeploymentRepair) AllFixed() bool {
	if rep.DryRun && rep.Fixed() > 0 {
		return false
	}
	return rep.Left() == 0 && rep.Failed() == 0
}

// Repair restores the desired state for key, object by object. It
// is the write half of Drift: drift first, plan second, apply only
// the creates/updates the plan asks for. Unexpected objects are
// reported, never deleted; a paused deployment is refused with no
// writes at all. A failed apply on one object does not stop the
// others. With dryRun, nothing is written and the report says what
// would change.
func (r *Reconciler) Repair(key string, dryRun bool) (DeploymentRepair, error) {
	ds, err := r.desired(key)
	if err != nil {
		return DeploymentRepair{}, err
	}
	before, err := r.Drift(key)
	if err != nil {
		return DeploymentRepair{}, err
	}

	rep := DeploymentRepair{Deployment: key, Paused: before.Paused, DryRun: dryRun}

	// A paused deployment is frozen: the pause is a promise the
	// live objects keep serving exactly as they are, and repair
	// must not break it. Report every offending object as left,
	// with the reason, and write nothing.
	if before.Paused {
		for _, res := range before.Resources {
			if res.Status == DriftInSync {
				rep.Results = append(rep.Results, ResourceRepair{Kind: res.Kind, Key: res.Key, Was: res.Status, Outcome: RepairUnchanged})
				continue
			}
			rep.Results = append(rep.Results, ResourceRepair{
				Kind: res.Kind, Key: res.Key, Was: res.Status, Outcome: RepairLeft,
				Detail: "paused — resume with `mlop resume " + key + "` before repairing",
			})
		}
		return rep, nil
	}

	// Unexpected objects are reported, never deleted. The plan
	// deletes them (a leftover canary, an HPA after autoscaling
	// was turned off); repair drops exactly those deletes and
	// records why, so the deletion stays a human decision.
	unexpected := map[string]bool{}
	for _, res := range before.Resources {
		if res.Status == DriftUnexpected {
			unexpected[res.Kind+"/"+res.Key] = true
		}
	}

	actions := r.plan(key, ds.d, ds.desired, ds.desiredSvc, ds.canary, ds.green, ds.desiredHPA, ds.hasHPA, ds.desiredPDB, ds.hasPDB)
	wasByKey := map[string]DriftStatus{}
	for _, res := range before.Resources {
		wasByKey[res.Kind+"/"+res.Key] = res.Status
	}

	// wasOf maps a plan action to the drift verdict of the object
	// it acts on, so the report can say what the object was.
	wasOf := func(a Action) DriftStatus {
		if s, ok := wasByKey[actionKind(a.Kind)+"/"+a.Key]; ok {
			return s
		}
		return DriftInSync
	}

	fixed := 0
	for _, a := range actions {
		if a.Kind == ActionNoOp {
			continue
		}
		kind := actionKind(a.Kind)
		if isDelete(a.Kind) && unexpected[kind+"/"+a.Key] {
			rep.Results = append(rep.Results, ResourceRepair{
				Kind: kind, Key: a.Key, Was: DriftUnexpected, Outcome: RepairLeft,
				Detail: "unexpected object — repair never deletes; remove it manually or update the manifest",
			})
			continue
		}
		if dryRun {
			rep.Results = append(rep.Results, ResourceRepair{
				Kind: kind, Key: a.Key, Was: wasOf(a), Outcome: RepairWouldFix, Detail: a.Reason,
			})
			fixed++
			continue
		}
		if err := r.retryApply(key, string(a.Kind), a.Key, func() error {
			return r.apply(a, ds.desired, ds.canary, ds.green, ds.desiredSvc, ds.desiredHPA, ds.desiredPDB)
		}); err != nil {
			rep.Results = append(rep.Results, ResourceRepair{
				Kind: kind, Key: a.Key, Was: wasOf(a), Outcome: RepairFailed, Detail: err.Error(),
			})
			continue
		}
		rep.Results = append(rep.Results, ResourceRepair{
			Kind: kind, Key: a.Key, Was: wasOf(a), Outcome: RepairFixed, Detail: a.Reason,
		})
		fixed++
	}

	// Verify: a repair that claims fixed must show in sync on a
	// fresh drift check. If the object is still off (a cluster
	// that silently dropped the write, a reason the plan did not
	// cover), say so instead of claiming success.
	if !dryRun && fixed > 0 {
		after, err := r.Drift(key)
		if err == nil {
			afterByKey := map[string]DriftStatus{}
			for _, res := range after.Resources {
				afterByKey[res.Kind+"/"+res.Key] = res.Status
			}
			for i, res := range rep.Results {
				if res.Outcome != RepairFixed {
					continue
				}
				if s, ok := afterByKey[res.Kind+"/"+res.Key]; ok && s != DriftInSync {
					rep.Results[i].Outcome = RepairFailed
					rep.Results[i].Detail = "apply succeeded but the object is still " + string(s) + " on re-check"
					fixed--
				}
			}
		}
	}

	// In-sync objects are not in the plan at all; list them as
	// unchanged so the report is a complete inventory, not just
	// the objects that moved.
	for _, res := range before.Resources {
		if res.Status != DriftInSync {
			continue
		}
		rep.Results = append(rep.Results, ResourceRepair{Kind: res.Kind, Key: res.Key, Was: res.Status, Outcome: RepairUnchanged})
	}
	sort.Slice(rep.Results, func(i, j int) bool {
		if rep.Results[i].Kind != rep.Results[j].Kind {
			return rep.Results[i].Kind < rep.Results[j].Kind
		}
		return rep.Results[i].Key < rep.Results[j].Key
	})

	if !dryRun && fixed > 0 {
		// History tracks generation/phase/image transitions, and
		// repair changes none of those — the audit trail entry
		// would be deduped away as the same transition — so the
		// repair is recorded as an event only, where it is
		// visible in `mlop logs`.
		msg := fmt.Sprintf("drift repaired: %d %s restored to desired state", fixed, plural(fixed, "object", "objects"))
		r.recordEvent(key, metrics.EventNormal, "DriftRepaired", msg)
	}
	return rep, nil
}

// RepairAll repairs every registered deployment, in key order. An
// invalid spec or unknown key anywhere aborts the whole run — the
// same fail-fast as DriftAll — so a bad manifest cannot hide behind
// a partial repair.
func (r *Reconciler) RepairAll(dryRun bool) ([]DeploymentRepair, error) {
	entries := r.Registry.List()
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Deployment.Key())
	}
	sort.Strings(keys)
	reports := make([]DeploymentRepair, 0, len(keys))
	for _, key := range keys {
		rep, err := r.Repair(key, dryRun)
		if err != nil {
			return nil, err
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

// actionKind maps a plan action to the resource kind it acts on.
func actionKind(k ActionKind) string {
	switch k {
	case ActionCreateDeployment, ActionUpdateDeployment, ActionDeleteDeployment:
		return "Deployment"
	case ActionCreateService, ActionUpdateService, ActionDeleteService:
		return "Service"
	case ActionCreateHPA, ActionUpdateHPA, ActionDeleteHPA:
		return "HPA"
	case ActionCreatePDB, ActionUpdatePDB, ActionDeletePDB:
		return "PodDisruptionBudget"
	}
	return string(k)
}

// isDelete reports whether the action removes an object.
func isDelete(k ActionKind) bool {
	switch k {
	case ActionDeleteDeployment, ActionDeleteService, ActionDeleteHPA, ActionDeletePDB:
		return true
	}
	return false
}
