// Drift detection: compare the live cluster against the desired
// state and report, per managed object, whether it is in sync,
// drifted, missing, or present-but-not-desired.
//
// Drift is what the reconciler would fix on the next apply, but
// seen from the other side: instead of an action plan it names the
// objects and the fields that moved. It exists for the human who
// changed something out of band (kubectl edit, a manual scale, a
// deleted Service) and wants to know what moved before deciding
// whether to re-apply or to update the manifest. Drift never
// mutates anything — it reads the desired state (the same
// computation Plan uses) and the live cluster, and reports.
//
// A deployment mid-rollout reports no drift for the fields the
// rollout itself is moving: desired() pins the stable image to the
// live image while a canary or blue-green is in flight, exactly as
// Plan does. A paused deployment still reports drift — the
// reconciler will not correct it until resume, and the report says
// so in the CLI.
package controller

import (
	"sort"
	"strings"

	"github.com/Karnchoudhary-99/ml-operator/internal/render"
)

// DriftStatus is the state of one managed object relative to the
// desired state.
type DriftStatus string

const (
	// DriftInSync means the live object matches the desired one.
	DriftInSync DriftStatus = "inSync"
	// DriftDrifted means the live object exists but managed fields
	// differ from the desired ones.
	DriftDrifted DriftStatus = "drifted"
	// DriftMissing means the desired object is absent from the
	// cluster entirely.
	DriftMissing DriftStatus = "missing"
	// DriftUnexpected means an object exists in the cluster that
	// the desired state does not ask for (a leftover canary, an
	// HPA after autoscaling was turned off).
	DriftUnexpected DriftStatus = "unexpected"
)

// ResourceDrift is the drift verdict for one managed object.
type ResourceDrift struct {
	// Kind is Deployment, Service, HPA, or PodDisruptionBudget.
	Kind string `json:"kind"`
	// Key is the "namespace/name" of the object.
	Key    string      `json:"key"`
	Status DriftStatus `json:"status"`
	// Reasons names what moved (live -> desired), one entry per
	// differing field; empty when in sync. For a missing object
	// there is a single reason; for an unexpected one, one too.
	Reasons []string `json:"reasons,omitempty"`
}

// DeploymentDrift is the drift report for one deployment.
type DeploymentDrift struct {
	Deployment string `json:"deployment"`
	// Paused reports the spec pause flag: drift on a paused
	// deployment is real, but nothing will correct it until
	// resume.
	Paused    bool            `json:"paused"`
	InSync    bool            `json:"inSync"`
	Resources []ResourceDrift `json:"resources"`
}

// Drift compares the live cluster against the desired state for
// key and reports per managed object. It is read-only: no cluster
// write, no status update, no rollout state change.
func (r *Reconciler) Drift(key string) (DeploymentDrift, error) {
	ds, err := r.desired(key)
	if err != nil {
		return DeploymentDrift{}, err
	}

	ns, name, _ := strings.Cut(key, "/")
	canaryKey := ns + "/" + name + "-canary"
	greenKey := ns + "/" + name + "-green"

	var resources []ResourceDrift
	resources = append(resources, r.deploymentDrift(ds.desired, key))
	if ds.canary != nil {
		resources = append(resources, r.deploymentDrift(*ds.canary, canaryKey))
	} else {
		resources = append(resources, r.unexpectedDeployment(canaryKey))
	}
	if ds.green != nil {
		resources = append(resources, r.deploymentDrift(*ds.green, greenKey))
	} else {
		resources = append(resources, r.unexpectedDeployment(greenKey))
	}
	resources = append(resources, r.serviceDrift(ds.desiredSvc, key))
	if ds.hasHPA {
		resources = append(resources, r.hpaDrift(ds.desiredHPA, key))
	} else {
		resources = append(resources, r.unexpectedHPA(key))
	}
	if ds.hasPDB {
		resources = append(resources, r.pdbDrift(ds.desiredPDB, key))
	} else {
		resources = append(resources, r.unexpectedPDB(key))
	}

	// Drop reason-less unexpected entries: they mean "checked,
	// absent" — not drift, not worth listing.
	kept := resources[:0]
	for _, res := range resources {
		if res.Status == DriftUnexpected && len(res.Reasons) == 0 {
			continue
		}
		kept = append(kept, res)
	}
	resources = kept

	inSync := true
	for _, res := range resources {
		if res.Status != DriftInSync {
			inSync = false
			break
		}
	}
	return DeploymentDrift{
		Deployment: key,
		Paused:     ds.d.Spec.Paused,
		InSync:     inSync,
		Resources:  resources,
	}, nil
}

// DriftAll reports drift for every registered deployment, in key
// order. An invalid spec anywhere aborts the whole report — the
// same fail-fast as Plan — so a bad manifest cannot hide behind a
// partial listing.
func (r *Reconciler) DriftAll() ([]DeploymentDrift, error) {
	entries := r.Registry.List()
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Deployment.Key())
	}
	sort.Strings(keys)
	reports := make([]DeploymentDrift, 0, len(keys))
	for _, key := range keys {
		rep, err := r.Drift(key)
		if err != nil {
			return nil, err
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

func (r *Reconciler) deploymentDrift(desired render.Deployment, key string) ResourceDrift {
	live, ok := r.Cluster.GetDeployment(key)
	if !ok {
		return ResourceDrift{Kind: "Deployment", Key: key, Status: DriftMissing,
			Reasons: []string{"deployment does not exist in the cluster"}}
	}
	if reasons := render.DeploymentDiffReasons(desired, live); len(reasons) > 0 {
		return ResourceDrift{Kind: "Deployment", Key: key, Status: DriftDrifted, Reasons: reasons}
	}
	return ResourceDrift{Kind: "Deployment", Key: key, Status: DriftInSync}
}

// unexpectedDeployment reports a canary/green Deployment that
// exists in the cluster although none is desired. Absent, it
// reports nothing: the caller drops a reason-less unexpected entry.
func (r *Reconciler) unexpectedDeployment(key string) ResourceDrift {
	if _, ok := r.Cluster.GetDeployment(key); !ok {
		return ResourceDrift{Kind: "Deployment", Key: key, Status: DriftUnexpected}
	}
	return ResourceDrift{Kind: "Deployment", Key: key, Status: DriftUnexpected,
		Reasons: []string{"deployment exists in the cluster but none is desired"}}
}

func (r *Reconciler) serviceDrift(desired render.Service, key string) ResourceDrift {
	live, ok := r.Cluster.GetService(key)
	if !ok {
		return ResourceDrift{Kind: "Service", Key: key, Status: DriftMissing,
			Reasons: []string{"service does not exist in the cluster"}}
	}
	if reasons := render.ServiceDiffReasons(desired, live); len(reasons) > 0 {
		return ResourceDrift{Kind: "Service", Key: key, Status: DriftDrifted, Reasons: reasons}
	}
	return ResourceDrift{Kind: "Service", Key: key, Status: DriftInSync}
}

func (r *Reconciler) hpaDrift(desired render.HPA, key string) ResourceDrift {
	live, ok := r.Cluster.GetHPA(key)
	if !ok {
		return ResourceDrift{Kind: "HPA", Key: key, Status: DriftMissing,
			Reasons: []string{"hpa does not exist in the cluster"}}
	}
	if reasons := render.HPADiffReasons(desired, live); len(reasons) > 0 {
		return ResourceDrift{Kind: "HPA", Key: key, Status: DriftDrifted, Reasons: reasons}
	}
	return ResourceDrift{Kind: "HPA", Key: key, Status: DriftInSync}
}

// unexpectedHPA reports a live HPA when autoscaling is off. Absent,
// it reports nothing: the caller drops a reason-less unexpected entry.
func (r *Reconciler) unexpectedHPA(key string) ResourceDrift {
	if _, ok := r.Cluster.GetHPA(key); !ok {
		return ResourceDrift{Kind: "HPA", Key: key, Status: DriftUnexpected}
	}
	return ResourceDrift{Kind: "HPA", Key: key, Status: DriftUnexpected,
		Reasons: []string{"hpa exists in the cluster but autoscaling is disabled"}}
}

func (r *Reconciler) pdbDrift(desired render.PodDisruptionBudget, key string) ResourceDrift {
	live, ok := r.Cluster.GetPDB(key)
	if !ok {
		return ResourceDrift{Kind: "PDB", Key: key, Status: DriftMissing,
			Reasons: []string{"poddisruptionbudget does not exist in the cluster"}}
	}
	if reasons := render.PDBDiffReasons(desired, live); len(reasons) > 0 {
		return ResourceDrift{Kind: "PDB", Key: key, Status: DriftDrifted, Reasons: reasons}
	}
	return ResourceDrift{Kind: "PDB", Key: key, Status: DriftInSync}
}

// unexpectedPDB reports a live PDB when none is desired (a single
// replica needs no disruption budget). Absent, it reports nothing:
// the caller drops a reason-less unexpected entry.
func (r *Reconciler) unexpectedPDB(key string) ResourceDrift {
	if _, ok := r.Cluster.GetPDB(key); !ok {
		return ResourceDrift{Kind: "PDB", Key: key, Status: DriftUnexpected}
	}
	return ResourceDrift{Kind: "PDB", Key: key, Status: DriftUnexpected,
		Reasons: []string{"poddisruptionbudget exists in the cluster but none is desired"}}
}
