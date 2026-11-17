package api

import "testing"

// TestDescribeImagePullSecrets pins the status rendering:
// names joined with ", ", an empty list reading "none".
func TestDescribeImagePullSecrets(t *testing.T) {
	if got := DescribeImagePullSecrets(nil); got != "none" {
		t.Fatalf("nil = %q, want %q", got, "none")
	}
	if got := DescribeImagePullSecrets([]string{}); got != "none" {
		t.Fatalf("empty = %q, want %q", got, "none")
	}
	if got := DescribeImagePullSecrets([]string{"regcred"}); got != "regcred" {
		t.Fatalf("one = %q, want %q", got, "regcred")
	}
	if got := DescribeImagePullSecrets([]string{"regcred", "ghcr-creds"}); got != "regcred, ghcr-creds" {
		t.Fatalf("two = %q, want %q", got, "regcred, ghcr-creds")
	}
}
