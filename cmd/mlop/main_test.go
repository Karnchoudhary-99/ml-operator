package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runCLI runs the CLI in-process with buffered output and a temp
// state file, returning the exit code and both streams.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	state := filepath.Join(t.TempDir(), "state.json")
	full := append([]string{"--state", state}, args...)
	code := run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// runCLIState runs the CLI against a fixed state file (shared
// across calls in one test) so multi-command flows can be tested.
func runCLIState(t *testing.T, state string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	full := append([]string{"--state", state}, args...)
	code := run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const irisV1 = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v1
    framework: onnx
    version: v1
  replicas: 2
`

func TestApplyAndStatusFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)

	code, out, _ := runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 {
		t.Fatalf("apply exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "applied default/iris-mlp (generation 1)") {
		t.Fatalf("apply output %q", out)
	}
	if !strings.Contains(out, "CreateDeployment") {
		t.Fatalf("apply should create the Deployment, got %q", out)
	}

	// A second apply of the same manifest is a no-op.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "unchanged default/iris-mlp (generation 1)") {
		t.Fatalf("re-apply exit %d, out %q", code, out)
	}

	// List status shows the deployment, still Progressing (no pods ready).
	code, out, _ = runCLIState(t, state, "status")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	if !strings.Contains(out, "iris-mlp") || !strings.Contains(out, "Progressing") || !strings.Contains(out, "0/2") {
		t.Fatalf("status list %q", out)
	}

	// Detail status shows the model and the requeue note.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 {
		t.Fatalf("status detail exit %d", code)
	}
	for _, want := range []string{"generation: 1", "ready:      0/2 replicas", "registry.example.com/iris-mlp:v1", "requeue:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status detail %q, want %q", out, want)
		}
	}

	// A new image bumps the generation and rolls.
	manifestV2 := writeManifest(t, strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:v2", 1))
	manifestV2 = strings.Replace(manifestV2, "version: v1", "version: v2", 1)
	code, out, _ = runCLIState(t, state, "apply", "-f", manifestV2)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 2)") {
		t.Fatalf("v2 apply exit %d, out %q", code, out)
	}
}

func TestApplyDryRunAndInvalid(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	code, out, _ := runCLIState(t, state, "apply", "-f", manifest, "--dry-run")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") {
		t.Fatalf("dry-run exit %d, out %q", code, out)
	}
	// A dry-run must not create state.
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a state file")
	}
	// A real apply after the dry-run still starts at generation 1.
	code, out, _ = runCLIState(t, state, "apply", "-f", manifest)
	if code != 0 || !strings.Contains(out, "(generation 1)") {
		t.Fatalf("apply after dry-run exit %d, out %q", code, out)
	}

	bad := writeManifest(t, "name: bad\nspec:\n  model:\n    image: \"\"\n")
	code, _, errOut := runCLI(t, "apply", "-f", bad)
	if code == 0 {
		t.Fatalf("invalid manifest should fail")
	}
	if !strings.Contains(errOut, "must not be empty") {
		t.Fatalf("invalid manifest error %q", errOut)
	}

	code, _, errOut = runCLI(t, "apply", "-f", filepath.Join(t.TempDir(), "missing.yaml"))
	if code == 0 {
		t.Fatalf("missing manifest file should fail")
	}
	if !strings.Contains(errOut, "read") {
		t.Fatalf("missing file error %q", errOut)
	}

	code, _, _ = runCLI(t, "apply")
	if code == 0 {
		t.Fatalf("apply without -f should fail")
	}
}

func TestApplyShowsWarnings(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	latest := writeManifest(t, strings.Replace(irisV1, "iris-mlp:v1", "iris-mlp:latest", 1))
	// A :latest apply still succeeds, but the warning is printed.
	code, out, _ := runCLIState(t, state, "apply", "-f", latest)
	if code != 0 || !strings.Contains(out, "applied default/iris-mlp (generation 1)") || !strings.Contains(out, ":latest tag") {
		t.Fatalf("apply :latest exit %d, out %q", code, out)
	}
	// The dry-run shows it too, before anything is applied.
	clean := writeManifest(t, irisV1)
	code, out, _ = runCLIState(t, state, "apply", "-f", clean, "--dry-run")
	if code != 0 || strings.Contains(out, "warning:") {
		t.Fatalf("dry-run clean exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "apply", "-f", latest, "--dry-run")
	if code != 0 || !strings.Contains(out, "valid: default/iris-mlp") || !strings.Contains(out, ":latest tag") {
		t.Fatalf("dry-run :latest exit %d, out %q", code, out)
	}
}

func TestStatusErrors(t *testing.T) {
	code, out, _ := runCLI(t, "status")
	if code != 0 || !strings.Contains(out, "no deployments") {
		t.Fatalf("empty status exit %d, out %q", code, out)
	}
	code, _, errOut := runCLI(t, "status", "nope")
	if code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("unknown name exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLI(t, "status", "a", "b")
	if code == 0 {
		t.Fatalf("status with two names should fail")
	}
}

const irisCanary = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v2
    framework: onnx
    version: v2
  replicas: 2
  strategy: Canary
  canary:
    weight: 10
    steps: 0
    intervalSeconds: 1
`

func TestReadyDrivesAvailable(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	// Flags after the name must work (reorderFlags).
	code, out, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2")
	if code != 0 || !strings.Contains(out, "phase=Available") {
		t.Fatalf("ready exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "phase:      Available") {
		t.Fatalf("status after ready exit %d, out %q", code, out)
	}
}

func TestReadyErrors(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	code, _, errOut := runCLIState(t, state, "ready", "nope", "--replicas", "1")
	if code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("ready unknown exit %d, err %q", code, errOut)
	}
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	// No canary Deployment exists for a RollingUpdate deployment.
	code, _, errOut = runCLIState(t, state, "ready", "iris-mlp", "--replicas", "1", "--canary")
	if code == 0 || !strings.Contains(errOut, "no Deployment") {
		t.Fatalf("ready --canary exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLIState(t, state, "ready", "iris-mlp")
	if code == 0 {
		t.Fatalf("ready without --replicas should fail")
	}
}

func TestRolloutStatusNoCanary(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "no canary rollout in flight") {
		t.Fatalf("rollout status exit %d, out %q", code, out)
	}
	code, _, _ = runCLIState(t, state, "rollout")
	if code == 0 {
		t.Fatalf("rollout without a subcommand should fail")
	}
	code, _, _ = runCLIState(t, state, "rollout", "status", "nope")
	if code == 0 {
		t.Fatalf("rollout status for an unknown deployment should fail")
	}
}

func TestCanaryFlowThroughCLI(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	v1 := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", v1); code != 0 {
		t.Fatalf("apply v1 failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready v1 failed")
	}
	// A plain apply records no events: only notable transitions do.
	code, out, _ := runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "no events") {
		t.Fatalf("logs after plain apply exit %d, out %q", code, out)
	}

	canary := writeManifest(t, irisCanary)
	code, out, _ = runCLIState(t, state, "apply", "-f", canary)
	if code != 0 || !strings.Contains(out, "(generation 2)") {
		t.Fatalf("canary apply exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "step:       1/2 (weight 10%)") {
		t.Fatalf("rollout status exit %d, out %q", code, out)
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary"); code != 0 {
		t.Fatalf("ready canary failed")
	}
	// The step interval is 1s: wait it out, then the rollout advances.
	time.Sleep(2 * time.Second)
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "weight 100%") {
		t.Fatalf("rollout advance exit %d, out %q", code, out)
	}
	time.Sleep(2 * time.Second)
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "state:      promoted") {
		t.Fatalf("rollout promote exit %d, out %q", code, out)
	}
	// The promotion is in the event log, and v2 is live.
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "CanaryPromoted") {
		t.Fatalf("logs after promote exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "registry.example.com/iris-mlp:v2") {
		t.Fatalf("status after promote exit %d, out %q", code, out)
	}
	// --tail limits the log.
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp", "--tail", "1")
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Fatalf("logs --tail 1 exit %d, out %q", code, out)
	}
}

func TestVersionAndUnknownCommand(t *testing.T) {
	code, out, _ := runCLI(t, "version")
	if code != 0 || !strings.Contains(out, "mlop ") {
		t.Fatalf("version exit %d, out %q", code, out)
	}
	code, _, errOut := runCLI(t, "frobnicate")
	if code == 0 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown command exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLI(t)
	if code == 0 {
		t.Fatalf("no command should fail")
	}
}

const irisV2Canary = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v2
    framework: onnx
    version: v2
  replicas: 2
  strategy: Canary
  canary:
    weight: 10
    steps: 0
    intervalSeconds: 1
`

const irisBlueGreen = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v2
    framework: onnx
    version: v2
  replicas: 2
  strategy: BlueGreen
  blueGreen:
    gate:
      maxErrorRate: 0.05
      maxP95LatencyMs: 500
      minReadyRatio: 1.0
`

const irisBlueGreenV1 = `
name: iris-mlp
spec:
  model:
    image: registry.example.com/iris-mlp:v1
    framework: onnx
    version: v1
  replicas: 2
  strategy: BlueGreen
`

func TestBlueGreenFlowThroughCLI(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	// A first deploy under BlueGreen has no version change in
	// flight: no green Deployment, no rollout to report.
	v1bg := writeManifest(t, irisBlueGreenV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", v1bg); code != 0 {
		t.Fatalf("apply blue-green v1 failed")
	}
	code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "no blue-green rollout in flight") {
		t.Fatalf("rollout status exit %d, out %q", code, out)
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready v1 failed")
	}

	bg := writeManifest(t, irisBlueGreen)
	code, out, _ = runCLIState(t, state, "apply", "-f", bg)
	if code != 0 || !strings.Contains(out, "(generation 2)") {
		t.Fatalf("blue-green apply exit %d, out %q", code, out)
	}
	// Green soaks: the rollout status reconciles first, and the
	// gate is pending (0 green pods ready).
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "state:      soaking") {
		t.Fatalf("rollout soaking exit %d, out %q", code, out)
	}
	// A diff before the cutover previews nothing left to do: the
	// green Deployment already exists and stable is pinned.
	code, out, _ = runCLIState(t, state, "diff", "iris-mlp")
	if code != 0 || !strings.Contains(out, "in sync") {
		t.Fatalf("diff exit %d, out %q", code, out)
	}
	// Green pods come up: the ready command's reconcile switches
	// the Service to green (phase Progressing — the fold lands on
	// the next reconcile).
	code, out, _ = runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--green")
	if code != 0 || !strings.Contains(out, "phase=Progressing") {
		t.Fatalf("ready green exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "BlueGreenSwitched") {
		t.Fatalf("logs after switch exit %d, out %q", code, out)
	}
	// The rollout status reconciles first, so it folds green into
	// stable and reports the fold.
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "state:      folded") {
		t.Fatalf("rollout fold exit %d, out %q", code, out)
	}
	// The fold is in the event log, and v2 is live.
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "BlueGreenPromoted") {
		t.Fatalf("logs after fold exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "registry.example.com/iris-mlp:v2") {
		t.Fatalf("status after fold exit %d, out %q", code, out)
	}
	// --canary and --green are mutually exclusive.
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary", "--green"); code == 0 {
		t.Fatalf("ready with both --canary and --green should fail")
	}
}
func TestRollbackFlow(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")

	// v1 applied and available.
	manifest := writeManifest(t, irisV1)
	if code, out, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply v1 exit %d, out %q", code, out)
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready exit %d", code)
	}

	// Nothing has replaced v1 yet: rollback refuses.
	if code, _, errOut := runCLIState(t, state, "rollback", "iris-mlp"); code == 0 {
		t.Fatal("rollback with no previous version succeeded")
	} else if !strings.Contains(errOut, "no previous version") {
		t.Fatalf("rollback error %q, want 'no previous version'", errOut)
	}

	// Canary v2, then promote it (the tour's timings: 1s step).
	canary := writeManifest(t, irisV2Canary)
	if code, out, _ := runCLIState(t, state, "apply", "-f", canary); code != 0 {
		t.Fatalf("apply canary exit %d, out %q", code, out)
	}
	// 2 ready, as in the tour: the canary is 1 replica at 10% and 2 at
	// 100%, so 2 ready passes the gate at both weights.
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary"); code != 0 {
		t.Fatalf("ready canary exit %d", code)
	}
	time.Sleep(2 * time.Second)
	if code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 || !strings.Contains(out, "weight 100%") {
		t.Fatalf("rollout status exit %d, out %q", code, out)
	}
	time.Sleep(2 * time.Second)
	if code, out, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 || !strings.Contains(out, "state:      promoted") {
		t.Fatalf("rollout promote exit %d, out %q", code, out)
	}

	// Status shows v2 live and v1 as the rollback target.
	if code, out, _ := runCLIState(t, state, "status", "iris-mlp"); code != 0 ||
		!strings.Contains(out, "registry.example.com/iris-mlp:v2") ||
		!strings.Contains(out, "previous:   registry.example.com/iris-mlp:v1 (v1)") {
		t.Fatalf("status after promote exit %d, out %q", code, out)
	}

	// Roll back: a new generation serving v1 again.
	if code, out, _ := runCLIState(t, state, "rollback", "iris-mlp"); code != 0 ||
		!strings.Contains(out, "rolled back default/iris-mlp (generation 3)") ||
		!strings.Contains(out, "now serving registry.example.com/iris-mlp:v1 (v1)") {
		t.Fatalf("rollback exit %d, out %q", code, out)
	}
	if code, out, _ := runCLIState(t, state, "status", "iris-mlp"); code != 0 ||
		!strings.Contains(out, "registry.example.com/iris-mlp:v1") {
		t.Fatalf("status after rollback exit %d, out %q", code, out)
	}
	// The rollback itself has not promoted yet, so there is no
	// newer previous version: a second rollback refuses.
	if code, _, errOut := runCLIState(t, state, "rollback", "iris-mlp"); code == 0 {
		t.Fatal("second rollback succeeded with nothing new to restore")
	} else if !strings.Contains(errOut, "already serves the previous version") {
		t.Fatalf("second rollback error %q", errOut)
	}
	// The rollback is in the event log.
	if code, out, _ := runCLIState(t, state, "logs", "iris-mlp"); code != 0 || !strings.Contains(out, "RolledBack") {
		t.Fatalf("logs exit %d, out %q", code, out)
	}
}

// TestLoadCommand checks `mlop load`: it reports the probe outcome
// for the stable Deployment, errors for unknown deployments and
// missing canaries, and drives a failed-load canary to abort
// through the CLI (three strikes, then Degraded with the stable
// Deployment still on v1).
func TestLoadCommand(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}

	code, out, _ := runCLIState(t, state, "load", "iris-mlp")
	if code != 0 || !strings.Contains(out, "model loaded for default/iris-mlp") {
		t.Fatalf("load exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "load", "iris-mlp", "--fail")
	if code != 0 || !strings.Contains(out, "model failed to load for default/iris-mlp") {
		t.Fatalf("load --fail exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "load", "iris-mlp", "--loading")
	if code != 0 || !strings.Contains(out, "model still loading for default/iris-mlp") {
		t.Fatalf("load --loading exit %d, out %q", code, out)
	}
	code, _, errOut := runCLIState(t, state, "load", "iris-mlp", "--loading", "--fail")
	if code == 0 || !strings.Contains(errOut, "mutually exclusive") {
		t.Fatalf("load --loading --fail exit %d, err %q", code, errOut)
	}
	code, _, errOut = runCLIState(t, state, "load", "nope")
	if code == 0 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("load unknown exit %d, err %q", code, errOut)
	}
	// No canary Deployment exists for a RollingUpdate deployment.
	code, _, errOut = runCLIState(t, state, "load", "iris-mlp", "--canary")
	if code == 0 || !strings.Contains(errOut, "no Deployment") {
		t.Fatalf("load --canary exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLIState(t, state, "load")
	if code == 0 {
		t.Fatalf("load without a name should fail")
	}
}

func TestLoadFailedCanaryAbortsThroughCLI(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	v1 := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", v1); code != 0 {
		t.Fatalf("apply v1 failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready v1 failed")
	}
	canary := writeManifest(t, irisCanary)
	if code, _, _ := runCLIState(t, state, "apply", "-f", canary); code != 0 {
		t.Fatalf("canary apply failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2", "--canary"); code != 0 {
		t.Fatalf("ready canary failed")
	}

	// Strike 1: the probe reports the model failed to load.
	code, out, _ := runCLIState(t, state, "load", "iris-mlp", "--canary", "--fail")
	if code != 0 || !strings.Contains(out, "model failed to load") || !strings.Contains(out, "phase=Progressing") {
		t.Fatalf("load --fail exit %d, out %q", code, out)
	}
	// Strikes 2 and 3 come from plain reconciles (rollout status
	// reconciles first): the third aborts the rollout.
	if code, _, _ := runCLIState(t, state, "rollout", "status", "iris-mlp"); code != 0 {
		t.Fatalf("rollout status (strike 2) failed")
	}
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "state:      aborted") {
		t.Fatalf("rollout status (strike 3) exit %d, out %q", code, out)
	}
	// The deployment is Degraded, the stable Deployment still
	// serves v1, and the abort is in the event log.
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "phase:      Degraded") || !strings.Contains(out, "registry.example.com/iris-mlp:v1") {
		t.Fatalf("status after abort exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "logs", "iris-mlp")
	if code != 0 || !strings.Contains(out, "RolloutAborted") {
		t.Fatalf("logs after abort exit %d, out %q", code, out)
	}
}
