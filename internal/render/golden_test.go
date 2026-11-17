package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// Golden files pin the rendered objects for the canonical iris-mlp
// deployment: any accidental change to what the operator would
// apply to a cluster shows up here first, with a readable diff.
// Regenerate with: go test ./internal/render/ -update
var updateGoldens = flag.Bool("update", false, "regenerate golden files")

// canonicalDeployment is the deployment every golden file renders
// from: iris-mlp v1, 2 replicas, autoscaling on, canary configured,
// env deliberately unsorted (rendering sorts it).
func canonicalDeployment() api.ModelDeployment {
	replicas := int32(2)
	d := api.ModelDeployment{
		Name:       "iris-mlp",
		Namespace:  "default",
		Generation: 1,
		Spec: api.ModelDeploymentSpec{
			Model:     api.ModelSpec{Image: "registry.example.com/iris-mlp:v1", Framework: "onnx", Version: "v1"},
			Replicas:  &replicas,
			Resources: api.ResourceRequirements{CPURequest: "500m", CPULimit: "1", MemoryRequest: "512Mi", MemoryLimit: "1Gi"},
			Port:      8080,
			Strategy:  api.StrategyCanary,
			Canary:    &api.CanarySpec{Weight: 10, Steps: 2, IntervalSeconds: 30},
			Autoscaling: &api.AutoscalingSpec{
				MinReplicas: 1, MaxReplicas: 4,
				TargetCPUUtilization: 70, TargetRequestsPerSecond: 100,
			},
			Env: map[string]string{
				"MODEL_PATH": "/models/iris-mlp",
				"BATCH_SIZE": "32",
			},
			Health: api.HealthSpec{Path: "/healthz", InitialDelaySeconds: 5, PeriodSeconds: 10},
		},
	}
	api.ApplyDefaults(&d)
	return d
}

func checkGolden(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", name)
	if *updateGoldens {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to generate)", path, err)
	}
	if string(raw) != string(want) {
		t.Fatalf("rendered %s changed:\n--- golden\n+++ got\n%s\n(run with -update to accept, after reviewing the diff)", name, diffLines(string(want), string(raw)))
	}
}

// diffLines renders a minimal unified-style diff for golden
// mismatches: unchanged lines are elided, changed lines are shown
// with - / + prefixes. It is a test helper, not a diff library.
func diffLines(want, got string) string {
	wantLines := splitLines(want)
	gotLines := splitLines(got)
	var out string
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		if w != "" {
			out += "- " + w + "\n"
		}
		if g != "" {
			out += "+ " + g + "\n"
		}
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestGoldenDeployment(t *testing.T) {
	checkGolden(t, "deployment.json", DeploymentFor(canonicalDeployment()))
}

func TestGoldenService(t *testing.T) {
	checkGolden(t, "service.json", ServiceFor(canonicalDeployment()))
}

func TestGoldenHPA(t *testing.T) {
	hpa, ok := HPAFor(canonicalDeployment())
	if !ok {
		t.Fatal("HPAFor returned no HPA for an autoscaling deployment")
	}
	checkGolden(t, "hpa.json", hpa)
}

func TestGoldenCanaryDeployment(t *testing.T) {
	checkGolden(t, "canary.json", CanaryDeploymentForWeight(canonicalDeployment(), 10))
}

func TestGoldenGreenDeployment(t *testing.T) {
	checkGolden(t, "green.json", GreenDeploymentFor(canonicalDeployment()))
}

// sourcedCanonicalDeployment is the canonical deployment with
// a model source: the same iris-mlp v1, 2 replicas, autoscaling
// on and canary configured, but the weights come from an https
// URL instead of the image.
func sourcedCanonicalDeployment() api.ModelDeployment {
	d := canonicalDeployment()
	d.Spec.ModelSource = &api.ModelSourceSpec{
		URI:    "https://artifacts.example.com/models/iris-mlp.onnx",
		SHA256: "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807",
	}
	return d
}

// TestGoldenSourcedDeployment pins the rendered objects for a
// sourced deployment: the init container, the shared volume
// and the serving container's mount on the stable, canary and
// green Deployments alike. Any accidental change to what the
// operator would apply shows up here first.
func TestGoldenSourcedDeployment(t *testing.T) {
	checkGolden(t, "sourced-deployment.json", DeploymentFor(sourcedCanonicalDeployment()))
}

func TestGoldenSourcedCanaryDeployment(t *testing.T) {
	checkGolden(t, "sourced-canary.json", CanaryDeploymentForWeight(sourcedCanonicalDeployment(), 10))
}

func TestGoldenSourcedGreenDeployment(t *testing.T) {
	checkGolden(t, "sourced-green.json", GreenDeploymentFor(sourcedCanonicalDeployment()))
}

// secretedCanonicalDeployment is the canonical deployment
// with image pull secrets: the same iris-mlp v1, 2 replicas,
// autoscaling on and canary configured, but the pods pull the
// image with registry credentials.
func secretedCanonicalDeployment() api.ModelDeployment {
	d := canonicalDeployment()
	d.Spec.ImagePullSecrets = []string{"regcred", "ghcr-creds"}
	return d
}

// TestGoldenSecretedDeployment pins the rendered objects for a
// deployment with pull secrets: the secret names on the pod
// template of the stable, canary and green Deployments alike.
// Any accidental change to what the operator would apply shows
// up here first.
func TestGoldenSecretedDeployment(t *testing.T) {
	checkGolden(t, "secreted-deployment.json", DeploymentFor(secretedCanonicalDeployment()))
}

func TestGoldenSecretedCanaryDeployment(t *testing.T) {
	checkGolden(t, "secreted-canary.json", CanaryDeploymentForWeight(secretedCanonicalDeployment(), 10))
}

func TestGoldenSecretedGreenDeployment(t *testing.T) {
	checkGolden(t, "secreted-green.json", GreenDeploymentFor(secretedCanonicalDeployment()))
}
