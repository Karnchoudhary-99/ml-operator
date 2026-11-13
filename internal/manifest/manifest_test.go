package manifest

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

const fullYAML = `
# a comment line
name: iris-mlp
namespace: staging
spec:
  model:
    image: registry.example.com/iris-mlp:v2
    framework: onnx
    version: v2
  replicas: 3
  resources:
    cpuRequest: 500m
    cpuLimit: "2"
    memoryRequest: 512Mi
    memoryLimit: 1Gi
  port: 8080
  strategy: Canary
  canary:
    weight: 10
    steps: 2
    intervalSeconds: 15
  autoscaling:
    minReplicas: 1
    maxReplicas: 5
    targetCPUUtilization: 70
  env:
    LOG_LEVEL: info
    MODEL_CACHE: /models
  health:
    path: /healthz
    initialDelaySeconds: 5
    periodSeconds: 10
`

func TestParseFullYAML(t *testing.T) {
	d, err := Parse([]byte(fullYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Name != "iris-mlp" || d.Namespace != "staging" {
		t.Errorf("identity = %q/%q, want iris-mlp/staging", d.Namespace, d.Name)
	}
	if d.Spec.Model.Image != "registry.example.com/iris-mlp:v2" {
		t.Errorf("image = %q", d.Spec.Model.Image)
	}
	if d.Spec.Model.Framework != "onnx" || d.Spec.Model.Version != "v2" {
		t.Errorf("model = %+v", d.Spec.Model)
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 3 {
		t.Errorf("replicas = %v, want 3", d.Spec.Replicas)
	}
	if d.Spec.Resources.CPULimit != "2" || d.Spec.Resources.MemoryLimit != "1Gi" {
		t.Errorf("resources = %+v", d.Spec.Resources)
	}
	if d.Spec.Port != 8080 || d.Spec.Strategy != api.StrategyCanary {
		t.Errorf("port/strategy = %d/%s", d.Spec.Port, d.Spec.Strategy)
	}
	if d.Spec.Canary == nil || d.Spec.Canary.Weight != 10 || d.Spec.Canary.Steps != 2 || d.Spec.Canary.IntervalSeconds != 15 {
		t.Errorf("canary = %+v", d.Spec.Canary)
	}
	if d.Spec.Autoscaling == nil || d.Spec.Autoscaling.MaxReplicas != 5 || d.Spec.Autoscaling.TargetCPUUtilization != 70 {
		t.Errorf("autoscaling = %+v", d.Spec.Autoscaling)
	}
	if d.Spec.Env["LOG_LEVEL"] != "info" || d.Spec.Env["MODEL_CACHE"] != "/models" {
		t.Errorf("env = %v", d.Spec.Env)
	}
	if d.Spec.Health.Path != "/healthz" || d.Spec.Health.PeriodSeconds != 10 {
		t.Errorf("health = %+v", d.Spec.Health)
	}
}

func TestParseMinimalYAML(t *testing.T) {
	d, err := Parse([]byte("name: tiny\nspec:\n  model:\n    image: img:v1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Name != "tiny" || d.Spec.Model.Image != "img:v1" {
		t.Errorf("parsed = %+v", d)
	}
	// Unset fields stay unset: defaults are the CLI's job, not the parser's.
	if d.Namespace != "" || d.Spec.Replicas != nil || d.Spec.Canary != nil {
		t.Errorf("unset fields leaked defaults: %+v", d)
	}
}

func TestParseJSON(t *testing.T) {
	d, err := Parse([]byte(`{"name":"j","namespace":"default","spec":{"model":{"image":"img:v1"},"replicas":2,"port":9000}}`))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	if d.Name != "j" || d.Spec.Port != 9000 || d.Spec.Replicas == nil || *d.Spec.Replicas != 2 {
		t.Errorf("parsed = %+v", d)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"empty", "", "empty"},
		{"unknown top key", "name: x\nbogus: 1\n", "unknown key"},
		{"unknown spec key", "name: x\nspec:\n  bogus: 1\n", "unknown key"},
		{"unknown model key", "name: x\nspec:\n  model:\n    image: i\n    bogus: 1\n", "unknown key"},
		{"bad replicas type", "name: x\nspec:\n  replicas: many\n", "must be a number"},
		{"fractional replicas", "name: x\nspec:\n  replicas: 1.5\n", "must be a number"},
		{"spec not a map", "name: x\nspec: 5\n", "must be a mapping"},
		{"env value not string", "name: x\nspec:\n  env:\n    N: 3\n", "must be a string"},
		{"list rejected", "name: x\nspec:\n  - a\n", "lists are not supported"},
		{"tab rejected", "name: x\nspec:\n\tmodel:\n\t  image: i\n", "tabs are not allowed"},
		{"duplicate key", "name: x\nname: y\n", "duplicate key"},
		{"no colon", "name x\n", "expected 'key: value'"},
		{"bad json", "{not json", "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			if err == nil {
				t.Fatalf("expected error for %q", tc.doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseQuotedValues(t *testing.T) {
	d, err := Parse([]byte("name: 'quoted # not a comment'\nspec:\n  model:\n    image: \"img:v1 # tag\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Name != "quoted # not a comment" {
		t.Errorf("name = %q", d.Name)
	}
	if d.Spec.Model.Image != "img:v1 # tag" {
		t.Errorf("image = %q", d.Spec.Model.Image)
	}
}
