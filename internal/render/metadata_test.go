package render

import (
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

func metadataDeployment() api.ModelDeployment {
	d := api.ModelDeployment{Name: "iris-mlp", Namespace: "default"}
	d.Spec.Model = api.ModelSpec{Image: "registry.example.com/iris-mlp:v2", Version: "v2"}
	d.Spec.Port = 8080
	d.Spec.Replicas = int32Ptr(2)
	d.Spec.Labels = map[string]string{"team": "search", "cost-center": "ml-platform"}
	d.Spec.Annotations = map[string]string{"owner": "team-search", "runbook": "https://example.com/runbooks/iris-mlp"}
	return d
}

func int32Ptr(v int32) *int32 { return &v }

func TestLabelsMergeCustomLabels(t *testing.T) {
	labels := Labels(metadataDeployment())
	if labels["team"] != "search" || labels["cost-center"] != "ml-platform" {
		t.Fatalf("custom labels missing from rendered labels: %v", labels)
	}
	// The operator's own labels survive the merge.
	if labels["app.kubernetes.io/name"] != "iris-mlp" || labels["ml-operator/managed-by"] != ManagedBy {
		t.Fatalf("base labels lost in the merge: %v", labels)
	}
	// Mutating the spec afterwards must not relabel the render.
	d := metadataDeployment()
	labels = Labels(d)
	d.Spec.Labels["team"] = "other"
	if labels["team"] != "search" {
		t.Fatalf("rendered labels aliased the spec map: %v", labels)
	}
}

func TestLabelsDoNotLeakIntoSelector(t *testing.T) {
	sel := Selector(metadataDeployment())
	if _, ok := sel["team"]; ok {
		t.Fatalf("custom label leaked into the selector: %v", sel)
	}
	if _, ok := sel["cost-center"]; ok {
		t.Fatalf("custom label leaked into the selector: %v", sel)
	}
}

func TestAnnotationsCopied(t *testing.T) {
	if got := Annotations(metadataDeployment()); len(got) != 2 || got["owner"] != "team-search" {
		t.Fatalf("annotations = %v", got)
	}
	d := api.ModelDeployment{Name: "iris-mlp"}
	if got := Annotations(d); got != nil {
		t.Fatalf("no annotations should render nil, got %v", got)
	}
}

func TestMetadataRenderedOnAllObjects(t *testing.T) {
	d := metadataDeployment()
	dep := DeploymentFor(d)
	if dep.Labels["team"] != "search" || dep.Annotations["owner"] != "team-search" {
		t.Fatalf("deployment metadata = labels %v annotations %v", dep.Labels, dep.Annotations)
	}
	svc := ServiceFor(d)
	if svc.Labels["team"] != "search" || svc.Annotations["runbook"] != "https://example.com/runbooks/iris-mlp" {
		t.Fatalf("service metadata = labels %v annotations %v", svc.Labels, svc.Annotations)
	}
	// Canary and green inherit the metadata from the stable render.
	canary := CanaryDeploymentFor(d)
	if canary.Labels["team"] != "search" || canary.Annotations["owner"] != "team-search" {
		t.Fatalf("canary metadata = labels %v annotations %v", canary.Labels, canary.Annotations)
	}
	green := GreenDeploymentFor(d)
	if green.Labels["team"] != "search" || green.Annotations["owner"] != "team-search" {
		t.Fatalf("green metadata = labels %v annotations %v", green.Labels, green.Annotations)
	}
}

func TestMetadataDiffReasons(t *testing.T) {
	d := metadataDeployment()
	desired := DeploymentFor(d)
	live := DeploymentFor(d)
	delete(live.Labels, "team")
	live.Annotations["owner"] = "someone-else"
	reasons := DeploymentDiffReasons(desired, live)
	want := map[string]bool{"labels changed": false, "annotations changed": false}
	for _, r := range reasons {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for r, seen := range want {
		if !seen {
			t.Fatalf("expected diff reason %q, got %v", r, reasons)
		}
	}
	if got := DeploymentDiffReasons(desired, DeploymentFor(d)); len(got) != 0 {
		t.Fatalf("identical deployments should have no diff reasons, got %v", got)
	}

	desiredSvc, liveSvc := ServiceFor(d), ServiceFor(d)
	delete(liveSvc.Labels, "cost-center")
	if got := ServiceDiffReasons(desiredSvc, liveSvc); len(got) != 1 || got[0] != "labels changed" {
		t.Fatalf("service diff reasons = %v", got)
	}
}
