package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrafficCommand(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	manifest := writeManifest(t, irisV1)
	if code, _, _ := runCLIState(t, state, "apply", "-f", manifest); code != 0 {
		t.Fatalf("apply failed")
	}
	if code, _, _ := runCLIState(t, state, "ready", "iris-mlp", "--replicas", "2"); code != 0 {
		t.Fatalf("ready failed")
	}

	// Missing --requests is a usage error.
	code, _, errOut := runCLIState(t, state, "traffic", "iris-mlp")
	if code != 2 || !strings.Contains(errOut, "--requests") {
		t.Fatalf("traffic without --requests exit %d, err %q", code, errOut)
	}
	// Zero requests, negative errors, more errors than requests,
	// and a negative p95 are all usage errors.
	for _, args := range [][]string{
		{"traffic", "iris-mlp", "--requests", "0"},
		{"traffic", "iris-mlp", "--requests", "-5"},
		{"traffic", "iris-mlp", "--requests", "100", "--errors", "-1"},
		{"traffic", "iris-mlp", "--requests", "100", "--errors", "101"},
		{"traffic", "iris-mlp", "--requests", "100", "--p95-ms", "-5"},
	} {
		if code, _, _ := runCLIState(t, state, args...); code != 2 {
			t.Fatalf("traffic %v exit %d, want 2", args, code)
		}
	}
	// --canary and --green are mutually exclusive, like load.
	code, _, errOut = runCLIState(t, state, "traffic", "iris-mlp", "--requests", "10", "--canary", "--green")
	if code != 2 || !strings.Contains(errOut, "mutually exclusive") {
		t.Fatalf("traffic --canary --green exit %d, err %q", code, errOut)
	}
	// Unknown deployment, and a canary that does not exist yet.
	code, _, errOut = runCLIState(t, state, "traffic", "nope", "--requests", "10")
	if code != 1 || !strings.Contains(errOut, "no deployment") {
		t.Fatalf("traffic unknown exit %d, err %q", code, errOut)
	}
	code, _, errOut = runCLIState(t, state, "traffic", "iris-mlp", "--requests", "10", "--canary")
	if code != 1 || !strings.Contains(errOut, "no Deployment") {
		t.Fatalf("traffic --canary exit %d, err %q", code, errOut)
	}
	code, _, _ = runCLIState(t, state, "traffic")
	if code == 0 {
		t.Fatalf("traffic without a name should fail")
	}

	// Happy path: one batch, batch rate and cumulative agree.
	code, out, _ := runCLIState(t, state, "traffic", "iris-mlp", "--requests", "1000", "--errors", "10", "--p95-ms", "180")
	if code != 0 {
		t.Fatalf("traffic exit %d", code)
	}
	for _, want := range []string{
		"default/iris-mlp: traffic 1000 requests, 10 errors (1.00% error rate, p95 180ms)",
		"cumulative 1000 requests, 10 errors (1.00%)",
		"phase=Available",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("traffic out %q, want %q", out, want)
		}
	}

	// A second batch accumulates: the cumulative rate is 40/2000,
	// not the second batch's 30/1000. The p95 is the latest
	// observation, so the first batch's 180ms still shows.
	code, out, _ = runCLIState(t, state, "traffic", "iris-mlp", "--requests", "1000", "--errors", "30")
	if code != 0 {
		t.Fatalf("second traffic exit %d", code)
	}
	for _, want := range []string{
		"traffic 1000 requests, 30 errors (3.00% error rate, p95 180ms)",
		"cumulative 2000 requests, 40 errors (2.00%)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("second traffic out %q, want %q", out, want)
		}
	}
}

// TestTrafficBadCanaryAbortsThroughCLI feeds a canary an error
// rate over its gate (the test manifest takes the 5% default) and
// proves the rollout aborts after three consecutive gate failures,
// with stable still serving v1.
func TestTrafficBadCanaryAbortsThroughCLI(t *testing.T) {
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

	// Strike 1: 100 requests, 20 errors — 20% against a 5% gate.
	code, out, _ := runCLIState(t, state, "traffic", "iris-mlp", "--canary", "--requests", "100", "--errors", "20", "--p95-ms", "180")
	if code != 0 || !strings.Contains(out, "20.00% error rate") || !strings.Contains(out, "phase=Progressing") {
		t.Fatalf("bad traffic exit %d, out %q", code, out)
	}
	// Strikes 2 and 3 come from plain reconciles (rollout status
	// reconciles first): the third aborts the rollout. The error
	// rate stays 20% — the observation persists in the metrics.
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

// TestTrafficHealthyCanaryPromotesThroughCLI is the mirror image:
// a canary fed clean traffic (0.5% errors against a 5% gate)
// promotes instead of aborting.
func TestTrafficHealthyCanaryPromotesThroughCLI(t *testing.T) {
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

	// 1000 requests, 5 errors: 0.5% against a 5% gate, p95 well
	// under the 500ms default — the gate passes.
	code, out, _ := runCLIState(t, state, "traffic", "iris-mlp", "--canary", "--requests", "1000", "--errors", "5", "--p95-ms", "180")
	if code != 0 || !strings.Contains(out, "0.50% error rate") {
		t.Fatalf("healthy traffic exit %d, out %q", code, out)
	}
	// The canary needs one interval (1s) before it can advance:
	// wait it out, then the next reconcile moves it to 100%;
	// promotion itself lands on the reconcile after that (the
	// same two-step rhythm the model-load tour drives).
	time.Sleep(2 * time.Second)
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "weight 100%") {
		t.Fatalf("rollout status after interval exit %d, out %q", code, out)
	}
	time.Sleep(2 * time.Second)
	code, out, _ = runCLIState(t, state, "rollout", "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "state:      promoted") {
		t.Fatalf("rollout status after promotion exit %d, out %q", code, out)
	}
	code, out, _ = runCLIState(t, state, "status", "iris-mlp")
	if code != 0 || !strings.Contains(out, "registry.example.com/iris-mlp:v2") {
		t.Fatalf("status after healthy traffic exit %d, out %q", code, out)
	}
}
