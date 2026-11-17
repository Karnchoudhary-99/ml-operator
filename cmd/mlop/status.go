package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/store"
)

// statusListView is one row of `mlop status --json`.
type statusListView struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Phase     api.Phase `json:"phase"`
	Ready     int32     `json:"ready"`
	Desired   int32     `json:"desired"`
	Version   string    `json:"version"`
	Message   string    `json:"message"`
}

// trafficView is the JSON form of one Deployment's recorded
// traffic observation: cumulative requests/errors, the error rate
// derived from them, and the latest p95. It is omitted when no
// traffic has been observed for that Deployment.
type trafficView struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"errorRate"`
	LatencyP95Ms float64 `json:"latencyP95Ms,omitempty"`
}

// describeTraffic renders one observation the way `mlop status`
// prints it: cumulative counts and rate, plus the latest p95 when
// one was reported.
func describeTraffic(m controller.ClusterMetrics) string {
	s := fmt.Sprintf("%d requests, %d errors (%.2f%% error rate)", m.Requests, m.Errors, 100*m.ErrorRate)
	if m.LatencyP95Ms > 0 {
		s += fmt.Sprintf(", p95 %.0fms", m.LatencyP95Ms)
	}
	return s
}

// statusDetailView is the JSON form of `mlop status <name> --json`.
type statusDetailView struct {
	Name               string                     `json:"name"`
	Namespace          string                     `json:"namespace"`
	Generation         int64                      `json:"generation"`
	ObservedGeneration int64                      `json:"observedGeneration"`
	Phase              api.Phase                  `json:"phase"`
	Ready              int32                      `json:"ready"`
	Desired            int32                      `json:"desired"`
	Image              string                     `json:"image"`
	Framework          string                     `json:"framework"`
	Version            string                     `json:"version"`
	PreviousImage      string                     `json:"previousImage,omitempty"`
	PreviousVersion    string                     `json:"previousVersion,omitempty"`
	Strategy           api.RolloutStrategy        `json:"strategy"`
	Paused             bool                       `json:"paused"`
	MaintenanceWindow  *api.MaintenanceWindowSpec `json:"maintenanceWindow,omitempty"`
	Autoscaling        *api.AutoscalingSpec       `json:"autoscaling,omitempty"`
	Availability       *api.AvailabilitySpec      `json:"availability,omitempty"`
	Security           *api.SecuritySpec          `json:"security,omitempty"`
	Placement          *api.PlacementSpec         `json:"placement,omitempty"`
	Traffic            *trafficView               `json:"traffic,omitempty"`
	CanaryTraffic      *trafficView               `json:"canaryTraffic,omitempty"`
	GreenTraffic       *trafficView               `json:"greenTraffic,omitempty"`
	Message            string                     `json:"message"`
	Conditions         []api.Condition            `json:"conditions,omitempty"`
	Requeue            bool                       `json:"requeue"`
}

// trafficOf returns the recorded traffic observation for one
// Deployment, or nil when none has been observed for it.
func trafficOf(rt *store.Runtime, target string) *trafficView {
	m, ok := rt.Cluster.Metrics(target)
	if !ok || m.Requests == 0 {
		return nil
	}
	return &trafficView{Requests: m.Requests, Errors: m.Errors, ErrorRate: m.ErrorRate, LatencyP95Ms: m.LatencyP95Ms}
}

// cmdStatus implements `mlop status [name] [--json]`: reconcile first (the
// operator is level-triggered, so this is a no-op when already in
// sync), then show the deployment phases. With no name it lists
// every deployment; with a name it shows the full detail view.
// A bare name means the "default" namespace; "ns/name" is accepted.
// --json prints the same data as JSON for scripts and dashboards.
func cmdStatus(opts *options, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(opts.stderr)
	fs.Usage = func() {
		fmt.Fprint(opts.stderr, "Usage: mlop status [name] [--json]\n")
	}
	jsonOut := fs.Bool("json", false, "print status as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(opts.stderr, "mlop status: at most one name")
		return 2
	}

	st, err := store.Load(opts.statePath)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
		return 1
	}
	rt := st.Restore()

	if fs.NArg() == 0 {
		entries := rt.Registry.List()
		if len(entries) == 0 {
			if *jsonOut {
				fmt.Fprintln(opts.stdout, "[]")
				return 0
			}
			fmt.Fprintln(opts.stdout, "no deployments")
			return 0
		}
		for _, e := range entries {
			if _, err := rt.Reconciler.Reconcile(e.Deployment.Key()); err != nil {
				fmt.Fprintf(opts.stderr, "mlop status: reconcile %s: %v\n", e.Deployment.Key(), err)
				return 1
			}
		}
		if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
			fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
			return 1
		}
		if *jsonOut {
			rows := make([]statusListView, 0, len(entries))
			for _, e := range rt.Registry.List() {
				d := e.Deployment
				rows = append(rows, statusListView{
					Name: d.Name, Namespace: namespaceOf(d), Phase: d.Status.Phase,
					Ready: d.Status.ReadyReplicas, Desired: desiredReplicas(d),
					Version: d.Spec.Model.Version, Message: d.Status.Message,
				})
			}
			enc := json.NewEncoder(opts.stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rows); err != nil {
				fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
				return 1
			}
			return 0
		}
		fmt.Fprintln(opts.stdout, "NAME\tNAMESPACE\tPHASE\tREADY\tVERSION\tMESSAGE")
		for _, e := range rt.Registry.List() {
			d := e.Deployment
			fmt.Fprintf(opts.stdout, "%s\t%s\t%s\t%d/%d\t%s\t%s\n",
				d.Name, namespaceOf(d), d.Status.Phase,
				d.Status.ReadyReplicas, desiredReplicas(d),
				d.Spec.Model.Version, d.Status.Message)
		}
		return 0
	}

	key := resolveKey(fs.Arg(0))
	entry := rt.Registry.Get(key)
	if entry == nil {
		fmt.Fprintf(opts.stderr, "mlop status: no deployment %q\n", key)
		return 1
	}
	res, err := rt.Reconciler.Reconcile(key)
	if err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: reconcile %s: %v\n", key, err)
		return 1
	}
	if err := store.Save(opts.statePath, store.From(rt.Registry, rt.Cluster, rt.Reconciler)); err != nil {
		fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
		return 1
	}
	d := rt.Registry.Get(key).Deployment
	ns, name, _ := strings.Cut(key, "/")
	traffic := trafficOf(rt, key)
	canaryTraffic := trafficOf(rt, ns+"/"+name+"-canary")
	greenTraffic := trafficOf(rt, ns+"/"+name+"-green")
	if *jsonOut {
		view := statusDetailView{
			Name: d.Name, Namespace: namespaceOf(d),
			Generation: d.Generation, ObservedGeneration: d.Status.ObservedGeneration,
			Phase: d.Status.Phase, Ready: d.Status.ReadyReplicas, Desired: desiredReplicas(d),
			Image: d.Spec.Model.Image, Framework: d.Spec.Model.Framework, Version: d.Spec.Model.Version,
			PreviousImage: d.Status.PreviousImage, PreviousVersion: d.Status.PreviousVersion,
			Strategy: d.Spec.Strategy, Paused: d.Spec.Paused, MaintenanceWindow: d.Spec.MaintenanceWindow,
			Autoscaling:  d.Spec.Autoscaling,
			Availability: d.Spec.Availability, Security: d.Spec.Security,
			Placement: d.Spec.Placement,
			Traffic:   traffic, CanaryTraffic: canaryTraffic, GreenTraffic: greenTraffic,
			Message: d.Status.Message, Conditions: d.Status.Conditions, Requeue: res.Requeue,
		}
		enc := json.NewEncoder(opts.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(view); err != nil {
			fmt.Fprintf(opts.stderr, "mlop status: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(opts.stdout, "name:       %s\n", d.Name)
	fmt.Fprintf(opts.stdout, "namespace:  %s\n", namespaceOf(d))
	fmt.Fprintf(opts.stdout, "generation: %d (observed %d)\n", d.Generation, d.Status.ObservedGeneration)
	fmt.Fprintf(opts.stdout, "phase:      %s\n", d.Status.Phase)
	fmt.Fprintf(opts.stdout, "ready:      %d/%d replicas\n", d.Status.ReadyReplicas, desiredReplicas(d))
	fmt.Fprintf(opts.stdout, "model:      %s (%s, %s)\n", d.Spec.Model.Image, d.Spec.Model.Framework, d.Spec.Model.Version)
	if d.Status.PreviousImage != "" {
		fmt.Fprintf(opts.stdout, "previous:   %s (%s) — mlop rollback restores this\n", d.Status.PreviousImage, d.Status.PreviousVersion)
	}
	fmt.Fprintf(opts.stdout, "strategy:   %s\n", d.Spec.Strategy)
	if d.Spec.Paused {
		fmt.Fprintf(opts.stdout, "paused:     yes — reconciliation suspended (mlop resume %s)\n", d.Name)
	}
	if w := d.Spec.MaintenanceWindow; w != nil {
		// The reconcile above already ran against the live clock,
		// so this line agrees with the WindowBlocked condition
		// printed below: open now, or closed with the next
		// opening named.
		if w.ActiveAt(time.Now()) {
			fmt.Fprintf(opts.stdout, "window:     %s (open now)\n", w.Describe())
		} else if next := w.NextOpening(time.Now()); !next.IsZero() {
			fmt.Fprintf(opts.stdout, "window:     %s (closed — next opens %s)\n", w.Describe(), next.Format("Mon 15:04 UTC"))
		} else {
			fmt.Fprintf(opts.stdout, "window:     %s (closed)\n", w.Describe())
		}
	}
	if d.Spec.Autoscaling != nil {
		fmt.Fprintf(opts.stdout, "autoscaling: min %d, max %d, cpu %d%%, rps %d\n",
			d.Spec.Autoscaling.MinReplicas, d.Spec.Autoscaling.MaxReplicas,
			d.Spec.Autoscaling.TargetCPUUtilization, d.Spec.Autoscaling.TargetRequestsPerSecond)
	}
	if d.Spec.Availability != nil {
		// Effective values, not raw spec: minAvailable 0 means
		// replicas-1 and maxSkew 0 means 1, and the line should
		// say what the cluster actually gets.
		fmt.Fprintf(opts.stdout, "availability: minAvailable %d/%d, anti-affinity %s, maxSkew %d\n",
			d.Spec.Availability.EffectiveMinAvailable(desiredReplicas(d)),
			desiredReplicas(d),
			onOff(d.Spec.Availability.AntiAffinityEnabled()),
			d.Spec.Availability.EffectiveMaxSkew())
	}
	if d.Spec.Security != nil {
		// Effective values again: the line says what the
		// cluster actually gets, and a relaxed deployment is
		// visible at a glance instead of hidden in the YAML.
		fmt.Fprintf(opts.stdout, "security:     runAsNonRoot %s, uid %d, read-only rootfs %s, privilege escalation %s, drop %s, seccomp %s\n",
			onOff(d.Spec.Security.RunAsNonRootEnabled()),
			d.Spec.Security.EffectiveRunAsUser(),
			onOff(d.Spec.Security.ReadOnlyRootFilesystemEnabled()),
			onOff(d.Spec.Security.AllowPrivilegeEscalationEnabled()),
			strings.Join(d.Spec.Security.EffectiveDropCapabilities(), ","),
			d.Spec.Security.EffectiveSeccompProfile())
	}
	if d.Spec.Placement != nil {
		// Placement has no defaults to resolve: the line says
		// where the pods are pinned, or that they are not.
		fmt.Fprintf(opts.stdout, "placement:    %s\n", d.Spec.Placement.Describe())
	}
	// Traffic lines appear only once traffic has been observed:
	// a deployment with no observations shows nothing here, and
	// a canary/green line appears while that rollout is in flight.
	if traffic != nil {
		m, _ := rt.Cluster.Metrics(key)
		fmt.Fprintf(opts.stdout, "traffic:     %s\n", describeTraffic(m))
	}
	if canaryTraffic != nil {
		m, _ := rt.Cluster.Metrics(ns + "/" + name + "-canary")
		fmt.Fprintf(opts.stdout, "traffic canary: %s\n", describeTraffic(m))
	}
	if greenTraffic != nil {
		m, _ := rt.Cluster.Metrics(ns + "/" + name + "-green")
		fmt.Fprintf(opts.stdout, "traffic green: %s\n", describeTraffic(m))
	}
	fmt.Fprintf(opts.stdout, "message:    %s\n", d.Status.Message)
	if len(d.Status.Conditions) > 0 {
		fmt.Fprintln(opts.stdout, "conditions:")
		for _, c := range d.Status.Conditions {
			fmt.Fprintf(opts.stdout, "  %s: %s (%s) %s — %s\n",
				c.Type, c.Status, c.Reason, c.Message, formatConditionAge(c.LastTransitionTime, time.Now()))
		}
	}
	if res.Requeue {
		fmt.Fprintln(opts.stdout, "requeue:    yes — still converging")
	}
	return 0
}

// formatConditionAge renders how long a condition has held its
// current status: "just now" under a minute, "5m ago", "2h ago",
// "3d ago" beyond that. A zero timestamp (state written before
// transition times existed, never reconciled since) reads as
// "unknown", never as a date in year 1.
func formatConditionAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// resolveKey turns a CLI name into a registry key: "name" means
// "default/name", "ns/name" is used as-is.
func resolveKey(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "default/" + name
}

func namespaceOf(d api.ModelDeployment) string {
	if d.Namespace == "" {
		return "default"
	}
	return d.Namespace
}

func desiredReplicas(d api.ModelDeployment) int32 {
	if d.Spec.Replicas == nil {
		return api.DefaultReplicas
	}
	return *d.Spec.Replicas
}

// onOff renders a boolean as "on"/"off" for status lines.
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
