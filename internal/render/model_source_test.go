package render

import (
	"strings"
	"testing"

	"github.com/Karnchoudhary-99/ml-operator/internal/api"
)

// sourcedDep returns a deployment whose model artifact comes
// from a model source: the weights live at an https URL, not
// in the image.
func sourcedDep() api.ModelDeployment {
	d := dep()
	d.Spec.ModelSource = &api.ModelSourceSpec{
		URI:    "https://artifacts.example.com/models/iris-mlp.onnx",
		SHA256: "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a291807",
	}
	return d
}

// TestRenderModelSource pins the rendered pod for a sourced
// deployment: one download init container (curl image, sh -c
// fetch + checksum verification), one shared emptyDir volume,
// the volume mounted into both containers, and the serving
// container told where the artifact landed.
func TestRenderModelSource(t *testing.T) {
	got := DeploymentFor(sourcedDep())

	if len(got.InitContainers) != 1 {
		t.Fatalf("InitContainers = %d, want 1", len(got.InitContainers))
	}
	init := got.InitContainers[0]
	if init.Name != "model-download" || init.Image != "curlimages/curl:8.5.0" {
		t.Fatalf("init container wrong: %+v", init)
	}
	if len(init.Command) != 3 || init.Command[0] != "sh" || init.Command[1] != "-c" {
		t.Fatalf("init command wrong: %v", init.Command)
	}
	script := init.Command[2]
	if !strings.Contains(script, `curl -fsSL "$MODEL_SOURCE_URI" -o "$MODEL_ARTIFACT_PATH"`) {
		t.Fatalf("init script %q, want the curl fetch", script)
	}
	if !strings.Contains(script, `sha256sum -c -`) {
		t.Fatalf("init script %q, want the checksum verification", script)
	}
	if got := envValue(init.Env, "MODEL_SOURCE_URI"); got != "https://artifacts.example.com/models/iris-mlp.onnx" {
		t.Fatalf("init MODEL_SOURCE_URI = %q", got)
	}
	if got := envValue(init.Env, "MODEL_SOURCE_SHA256"); got == "" {
		t.Fatal("init MODEL_SOURCE_SHA256 is missing")
	}
	if len(init.VolumeMounts) != 1 || init.VolumeMounts[0] != (VolumeMount{Name: "model-artifacts", MountPath: "/models"}) {
		t.Fatalf("init mounts wrong: %+v", init.VolumeMounts)
	}

	if len(got.Volumes) != 1 || got.Volumes[0] != (Volume{Name: "model-artifacts", EmptyDir: true}) {
		t.Fatalf("volumes wrong: %+v", got.Volumes)
	}

	c := got.Container
	if len(c.VolumeMounts) != 1 || c.VolumeMounts[0] != (VolumeMount{Name: "model-artifacts", MountPath: "/models"}) {
		t.Fatalf("serving container mounts wrong: %+v", c.VolumeMounts)
	}
	if got := envValue(c.Env, "MODEL_ARTIFACT_PATH"); got != "/models/iris-mlp.onnx" {
		t.Fatalf("serving MODEL_ARTIFACT_PATH = %q, want /models/iris-mlp.onnx", got)
	}
	// The canary Deployment inherits the init container and
	// the volume: a rollout never serves without its artifact.
	canary := CanaryDeploymentFor(sourcedDep())
	if len(canary.InitContainers) != 1 || len(canary.Volumes) != 1 || len(canary.Container.VolumeMounts) != 1 {
		t.Fatalf("canary lost the model source: %+v", canary)
	}
}

// TestRenderModelSourceUnverified pins that a source without
// a checksum renders a download-only script: no verification
// step, and no MODEL_SOURCE_SHA256 in the init container.
func TestRenderModelSourceUnverified(t *testing.T) {
	d := dep()
	d.Spec.ModelSource = &api.ModelSourceSpec{URI: "https://artifacts.example.com/models/iris-mlp.onnx"}
	got := DeploymentFor(d)
	if len(got.InitContainers) != 1 {
		t.Fatalf("InitContainers = %d, want 1", len(got.InitContainers))
	}
	script := got.InitContainers[0].Command[2]
	if strings.Contains(script, "sha256sum") {
		t.Fatalf("unverified script %q, want no checksum step", script)
	}
	if got := envValue(got.InitContainers[0].Env, "MODEL_SOURCE_SHA256"); got != "" {
		t.Fatalf("unverified init container sets MODEL_SOURCE_SHA256 = %q", got)
	}
}

// TestRenderModelSourceUserEnvWins pins that the author's own
// MODEL_ARTIFACT_PATH is never overridden: the operator
// mounts the volume either way, but the serving container
// keeps the explicit path.
func TestRenderModelSourceUserEnvWins(t *testing.T) {
	d := sourcedDep()
	d.Spec.Env["MODEL_ARTIFACT_PATH"] = "/custom/weights.onnx"
	got := DeploymentFor(d)
	if got := envValue(got.Container.Env, "MODEL_ARTIFACT_PATH"); got != "/custom/weights.onnx" {
		t.Fatalf("user MODEL_ARTIFACT_PATH = %q, want the author's value", got)
	}
	count := 0
	for _, e := range got.Container.Env {
		if e.Name == "MODEL_ARTIFACT_PATH" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("MODEL_ARTIFACT_PATH appears %d times, want 1", count)
	}
	if len(got.Container.VolumeMounts) != 1 {
		t.Fatalf("volume mount missing: %+v", got.Container.VolumeMounts)
	}
}

// TestRenderNoModelSource pins the default: without a model
// source there is no init container, no volume, and no mount
// — the image carries the model, as before.
func TestRenderNoModelSource(t *testing.T) {
	got := DeploymentFor(dep())
	if len(got.InitContainers) != 0 || len(got.Volumes) != 0 {
		t.Fatalf("unsourced deployment renders init/volumes: %+v / %+v", got.InitContainers, got.Volumes)
	}
	if len(got.Container.VolumeMounts) != 0 {
		t.Fatalf("unsourced container mounts volumes: %+v", got.Container.VolumeMounts)
	}
	if got := envValue(got.Container.Env, "MODEL_ARTIFACT_PATH"); got != "" {
		t.Fatalf("unsourced container sets MODEL_ARTIFACT_PATH = %q", got)
	}
}

func envValue(env []EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

// TestDeploymentDiffReasonsModelSource pins that a model
// source change is a Deployment change: adding a source names
// the init container, the volume and the mount; changing only
// the URI names the init container (the download target
// moved); an identical source is in sync.
func TestDeploymentDiffReasonsModelSource(t *testing.T) {
	plain := DeploymentFor(dep())
	sourced := DeploymentFor(sourcedDep())
	reasons := DeploymentDiffReasons(sourced, plain)
	contains := func(want string) bool {
		for _, r := range reasons {
			if r == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"model download init container changed",
		"model artifact volume changed",
		"model artifact mount changed",
	} {
		if !contains(want) {
			t.Fatalf("reasons %v, want %q", reasons, want)
		}
	}
	// Same source on both sides: in sync.
	if reasons := DeploymentDiffReasons(sourced, DeploymentFor(sourcedDep())); len(reasons) != 0 {
		t.Fatalf("identical sources diff: %v", reasons)
	}
	// A different URI changes only the init container: the
	// volume and the mount are the same.
	other := sourcedDep()
	other.Spec.ModelSource.URI = "https://artifacts.example.com/models/iris-mlp-v2.onnx"
	reasons = DeploymentDiffReasons(DeploymentFor(other), sourced)
	if !contains("model download init container changed") {
		t.Fatalf("reasons %v, want the init container change", reasons)
	}
	if contains("model artifact volume changed") || contains("model artifact mount changed") {
		t.Fatalf("reasons %v, volume and mount did not change", reasons)
	}
}
