package render

import (
	"testing"
)

// TestRenderImagePullSecrets pins the rendered pod for a
// deployment with pull secrets: the names land on the pod
// template, the list is a copy (mutating the spec afterwards
// cannot move an already-rendered Deployment), and the canary
// Deployment inherits them — a rollout never fails to pull
// the new image for want of credentials.
func TestRenderImagePullSecrets(t *testing.T) {
	d := dep()
	d.Spec.ImagePullSecrets = []string{"regcred", "ghcr-creds"}
	got := DeploymentFor(d)
	if len(got.ImagePullSecrets) != 2 || got.ImagePullSecrets[0] != "regcred" || got.ImagePullSecrets[1] != "ghcr-creds" {
		t.Fatalf("ImagePullSecrets = %v", got.ImagePullSecrets)
	}
	d.Spec.ImagePullSecrets[0] = "tampered"
	if got.ImagePullSecrets[0] != "regcred" {
		t.Fatalf("rendered Deployment aliases the spec slice: %v", got.ImagePullSecrets)
	}
	canary := CanaryDeploymentFor(d)
	if len(canary.ImagePullSecrets) != 2 || canary.ImagePullSecrets[1] != "ghcr-creds" {
		t.Fatalf("canary lost the pull secrets: %v", canary.ImagePullSecrets)
	}
}

// TestRenderNoImagePullSecrets pins the default: without
// pull secrets the rendered Deployment carries none — the
// pods pull without credentials, as before.
func TestRenderNoImagePullSecrets(t *testing.T) {
	got := DeploymentFor(dep())
	if got.ImagePullSecrets != nil {
		t.Fatalf("unsourced ImagePullSecrets = %v, want nil", got.ImagePullSecrets)
	}
}

// TestDeploymentDiffReasonsImagePullSecrets pins that a
// pull-secret change is a Deployment change: adding a secret
// names it, rotating one names it, and an identical list is
// in sync. Order matters here — the rendered list keeps the
// spec's order — so a re-ordered list is a change too.
func TestDeploymentDiffReasonsImagePullSecrets(t *testing.T) {
	with := func(secrets []string) Deployment {
		d := dep()
		d.Spec.ImagePullSecrets = secrets
		return DeploymentFor(d)
	}
	plain := with(nil)
	secreted := with([]string{"regcred"})
	reasons := DeploymentDiffReasons(secreted, plain)
	found := false
	for _, r := range reasons {
		if r == "imagePullSecrets changed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons %v, want the imagePullSecrets change", reasons)
	}
	if reasons := DeploymentDiffReasons(secreted, with([]string{"regcred"})); len(reasons) != 0 {
		t.Fatalf("identical secrets diff: %v", reasons)
	}
	reasons = DeploymentDiffReasons(with([]string{"ghcr-creds"}), secreted)
	found = false
	for _, r := range reasons {
		if r == "imagePullSecrets changed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons %v, want the imagePullSecrets change on rotation", reasons)
	}
}
