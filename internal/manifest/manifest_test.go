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
  availability:
    minAvailable: 2
    antiAffinity: false
    maxSkew: 2
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
	if d.Spec.Availability == nil || d.Spec.Availability.MinAvailable != 2 || d.Spec.Availability.MaxSkew != 2 {
		t.Errorf("availability = %+v", d.Spec.Availability)
	}
	if d.Spec.Availability == nil || d.Spec.Availability.AntiAffinity == nil || *d.Spec.Availability.AntiAffinity {
		t.Errorf("availability antiAffinity = %+v, want explicit false", d.Spec.Availability)
	}
	if d.Spec.Env["LOG_LEVEL"] != "info" || d.Spec.Env["MODEL_CACHE"] != "/models" {
		t.Errorf("env = %v", d.Spec.Env)
	}
	if d.Spec.Health.Path != "/healthz" || d.Spec.Health.PeriodSeconds != 10 {
		t.Errorf("health = %+v", d.Spec.Health)
	}
}

func TestParseCanaryGate(t *testing.T) {
	doc := "name: iris-mlp\nspec:\n  model:\n    image: img:v2\n  strategy: Canary\n  canary:\n    weight: 10\n    steps: 0\n    intervalSeconds: 5\n    gate:\n      maxErrorRate: 0.02\n      maxP95LatencyMs: 250\n      minReadyRatio: 0.8\n"
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Spec.Canary == nil || d.Spec.Canary.Gate == nil {
		t.Fatalf("gate not parsed: %+v", d.Spec.Canary)
	}
	g := d.Spec.Canary.Gate
	if g.MaxErrorRate != 0.02 || g.MaxP95LatencyMs != 250 || g.MinReadyRatio != 0.8 {
		t.Fatalf("gate = %+v", g)
	}

	// A gate block with only one threshold parses; the rest is
	// ApplyDefaults' job, not the parser's.
	d, err = Parse([]byte("name: x\nspec:\n  strategy: Canary\n  canary:\n    weight: 10\n    gate:\n      maxErrorRate: 0.1\n"))
	if err != nil {
		t.Fatalf("Parse partial gate: %v", err)
	}
	if g := d.Spec.Canary.Gate; g.MaxErrorRate != 0.1 || g.MaxP95LatencyMs != 0 || g.MinReadyRatio != 0 {
		t.Fatalf("partial gate = %+v", g)
	}

	// Whole-number thresholds (JSON numbers) parse as floats.
	d, err = Parse([]byte(`{"name":"x","spec":{"strategy":"Canary","canary":{"weight":10,"gate":{"maxP95LatencyMs":250}}}}`))
	if err != nil {
		t.Fatalf("Parse JSON gate: %v", err)
	}
	if g := d.Spec.Canary.Gate; g.MaxP95LatencyMs != 250 {
		t.Fatalf("JSON gate = %+v", g)
	}
}

func TestParseBlueGreenGate(t *testing.T) {
	doc := "name: iris-mlp\nspec:\n  model:\n    image: img:v2\n  strategy: BlueGreen\n  blueGreen:\n    gate:\n      maxErrorRate: 0.02\n      maxP95LatencyMs: 250\n      minReadyRatio: 0.8\n"
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Spec.Strategy != api.StrategyBlueGreen {
		t.Fatalf("strategy = %q, want BlueGreen", d.Spec.Strategy)
	}
	if d.Spec.BlueGreen == nil || d.Spec.BlueGreen.Gate == nil {
		t.Fatalf("gate not parsed: %+v", d.Spec.BlueGreen)
	}
	g := d.Spec.BlueGreen.Gate
	if g.MaxErrorRate != 0.02 || g.MaxP95LatencyMs != 250 || g.MinReadyRatio != 0.8 {
		t.Fatalf("gate = %+v", g)
	}

	// A BlueGreen strategy with no blueGreen block parses too: the
	// gate defaults are ApplyDefaults' job, not the parser's.
	d, err = Parse([]byte("name: x\nspec:\n  strategy: BlueGreen\n"))
	if err != nil {
		t.Fatalf("Parse bare BlueGreen: %v", err)
	}
	if d.Spec.BlueGreen != nil {
		t.Fatalf("bare BlueGreen gained a spec: %+v", d.Spec.BlueGreen)
	}

	// An unknown key inside blueGreen fails loudly.
	if _, err := Parse([]byte("name: x\nspec:\n  strategy: BlueGreen\n  blueGreen:\n    bogus: 1\n")); err == nil {
		t.Fatal("expected an error for an unknown blueGreen key")
	}
	// A non-mapping gate fails loudly.
	if _, err := Parse([]byte("name: x\nspec:\n  strategy: BlueGreen\n  blueGreen:\n    gate: 5\n")); err == nil {
		t.Fatal("expected an error for a non-mapping blueGreen gate")
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

// TestParseSecurity pins the security block: a partial block
// parses (the rest is ApplyDefaults' job), the YAML dialect
// writes dropCapabilities as a comma-separated string, and the
// JSON form carries a real array.
func TestParseSecurity(t *testing.T) {
	d, err := Parse([]byte("name: x\nspec:\n  security:\n    runAsUser: 20000\n    dropCapabilities: NET_BIND_SERVICE, SYS_TIME\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := d.Spec.Security
	if s == nil || s.RunAsUser == nil || *s.RunAsUser != 20000 || s.RunAsNonRoot != nil {
		t.Fatalf("partial security = %+v", s)
	}
	if len(s.DropCapabilities) != 2 || s.DropCapabilities[0] != "NET_BIND_SERVICE" || s.DropCapabilities[1] != "SYS_TIME" {
		t.Fatalf("YAML dropCapabilities = %v, want [NET_BIND_SERVICE SYS_TIME]", s.DropCapabilities)
	}
	d, err = Parse([]byte(`{"name":"x","spec":{"security":{"runAsNonRoot":false,"readOnlyRootFilesystem":false,"dropCapabilities":["ALL"],"seccompProfile":"Unconfined"}}}`))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	s = d.Spec.Security
	if s == nil || s.RunAsNonRoot == nil || *s.RunAsNonRoot || s.ReadOnlyRootFilesystem == nil || *s.ReadOnlyRootFilesystem {
		t.Fatalf("JSON security = %+v", s)
	}
	if len(s.DropCapabilities) != 1 || s.DropCapabilities[0] != "ALL" || s.SeccompProfile != "Unconfined" {
		t.Fatalf("JSON security caps/profile = %+v", s)
	}
}

// TestParseAvailability pins the availability block: a partial
// block parses (the rest is ApplyDefaults' job), and the JSON form
// carries the boolean through.
func TestParseAvailability(t *testing.T) {
	d, err := Parse([]byte("name: x\nspec:\n  availability:\n    minAvailable: 1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a := d.Spec.Availability
	if a == nil || a.MinAvailable != 1 || a.AntiAffinity != nil || a.MaxSkew != 0 {
		t.Fatalf("partial availability = %+v", a)
	}
	d, err = Parse([]byte(`{"name":"x","spec":{"availability":{"antiAffinity":true,"maxSkew":2}}}`))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	a = d.Spec.Availability
	if a == nil || a.AntiAffinity == nil || !*a.AntiAffinity || a.MaxSkew != 2 {
		t.Fatalf("JSON availability = %+v", a)
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
		{"unknown gate key", "name: x\nspec:\n  strategy: Canary\n  canary:\n    weight: 10\n    gate:\n      bogus: 1\n", "unknown key"},
		{"unknown availability key", "name: x\nspec:\n  availability:\n    bogus: 1\n", "unknown key"},
		{"availability not a map", "name: x\nspec:\n  availability: 5\n", "must be a mapping"},
		{"bad availability bool", "name: x\nspec:\n  availability:\n    antiAffinity: maybe\n", "must be a boolean"},
		{"bad availability int", "name: x\nspec:\n  availability:\n    maxSkew: many\n", "must be a number"},
		{"unknown security key", "name: x\nspec:\n  security:\n    bogus: 1\n", "unknown key"},
		{"security not a map", "name: x\nspec:\n  security: 5\n", "must be a mapping"},
		{"bad security bool", "name: x\nspec:\n  security:\n    runAsNonRoot: maybe\n", "must be a boolean"},
		{"bad security uid", "name: x\nspec:\n  security:\n    runAsUser: many\n", "must be a number"},
		{"bad security caps", "name: x\nspec:\n  security:\n    dropCapabilities: 5\n", "must be a comma-separated string or a list of strings"},
		{"gate not a map", "name: x\nspec:\n  strategy: Canary\n  canary:\n    weight: 10\n    gate: 5\n", "must be a mapping"},
		{"bad gate value", "name: x\nspec:\n  strategy: Canary\n  canary:\n    weight: 10\n    gate:\n      maxErrorRate: high\n", "must be a number"},
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

// TestParsePaused: spec.paused parses from YAML and JSON, defaults
// to false when absent, and a non-boolean value is a parse error.
func TestParsePaused(t *testing.T) {
	d, err := Parse([]byte("name: x\nspec:\n  paused: true\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !d.Spec.Paused {
		t.Fatal("paused: true should parse as Paused=true")
	}
	d, err = Parse([]byte(`{"name":"x","spec":{"paused":true}}`))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	if !d.Spec.Paused {
		t.Fatal("JSON paused: true should parse as Paused=true")
	}
	d, err = Parse([]byte("name: x\nspec:\n  model:\n    image: img:v1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Spec.Paused {
		t.Fatal("absent paused should default to false")
	}
	if _, err := Parse([]byte("name: x\nspec:\n  paused: sometimes\n")); err == nil {
		t.Fatal("non-boolean paused should be a parse error")
	}
}

// TestParseMaintenanceWindow pins the maintenanceWindow block: the
// YAML dialect has no lists, so days arrive comma-separated; JSON
// carries a real array. A partial block parses (validation is
// ApplyDefaults + Validate's job, not the parser's).
func TestParseMaintenanceWindow(t *testing.T) {
	d, err := Parse([]byte("name: x\nspec:\n  maintenanceWindow:\n    days: Mon, Wed, Fri\n    start: 02:00\n    end: 04:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	w := d.Spec.MaintenanceWindow
	if w == nil || w.Start != "02:00" || w.End != "04:00" {
		t.Fatalf("YAML window = %+v", w)
	}
	if len(w.Days) != 3 || w.Days[0] != "Mon" || w.Days[1] != "Wed" || w.Days[2] != "Fri" {
		t.Fatalf("YAML days = %v, want [Mon Wed Fri]", w.Days)
	}
	d, err = Parse([]byte(`{"name":"x","spec":{"maintenanceWindow":{"days":["Sat","Sun"],"start":"22:00","end":"02:00"}}}`))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	w = d.Spec.MaintenanceWindow
	if w == nil || len(w.Days) != 2 || w.Days[0] != "Sat" || w.Start != "22:00" || w.End != "02:00" {
		t.Fatalf("JSON window = %+v", w)
	}
	d, err = Parse([]byte("name: x\nspec:\n  maintenanceWindow:\n    start: 02:00\n    end: 04:00\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	w = d.Spec.MaintenanceWindow
	if w == nil || len(w.Days) != 0 || w.Start != "02:00" {
		t.Fatalf("window without days = %+v, want empty days (every day)", w)
	}
	if _, err := Parse([]byte("name: x\nspec:\n  maintenanceWindow:\n    days: Mon, Tue\n    start: 02:00\n    end: 04:00\n    timezone: UTC\n")); err == nil {
		t.Fatal("unknown maintenanceWindow key should be a parse error")
	}
	if _, err := Parse([]byte("name: x\nspec:\n  maintenanceWindow: nightly\n")); err == nil {
		t.Fatal("non-mapping maintenanceWindow should be a parse error")
	}
}
