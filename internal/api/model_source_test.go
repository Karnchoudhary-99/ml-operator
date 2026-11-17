package api

import "testing"

// TestModelSourceDefaults pins the effective values: an empty
// mount path lands the artifact in /models and an empty
// downloader image resolves to the curl image.
func TestModelSourceDefaults(t *testing.T) {
	m := &ModelSourceSpec{URI: "https://artifacts.example.com/models/iris-mlp.onnx"}
	if got := m.EffectiveMountPath(); got != "/models" {
		t.Fatalf("EffectiveMountPath = %q, want %q", got, "/models")
	}
	if got := m.EffectiveDownloaderImage(); got != "curlimages/curl:8.5.0" {
		t.Fatalf("EffectiveDownloaderImage = %q, want %q", got, "curlimages/curl:8.5.0")
	}
	if got := m.ArtifactFileName(); got != "iris-mlp.onnx" {
		t.Fatalf("ArtifactFileName = %q, want %q", got, "iris-mlp.onnx")
	}
	if got := m.ArtifactPath(); got != "/models/iris-mlp.onnx" {
		t.Fatalf("ArtifactPath = %q, want %q", got, "/models/iris-mlp.onnx")
	}
}

// TestModelSourceExplicitValues pins that explicit mount paths
// and downloader images win over the defaults, and that a
// trailing slash on the mount path does not double the slash
// in the artifact path.
func TestModelSourceExplicitValues(t *testing.T) {
	m := &ModelSourceSpec{
		URI:             "https://artifacts.example.com/models/v2/iris-mlp.onnx",
		MountPath:       "/data/models/",
		DownloaderImage: "curlimages/curl:8.9.0",
	}
	if got := m.EffectiveMountPath(); got != "/data/models/" {
		t.Fatalf("EffectiveMountPath = %q, want %q", got, "/data/models/")
	}
	if got := m.EffectiveDownloaderImage(); got != "curlimages/curl:8.9.0" {
		t.Fatalf("EffectiveDownloaderImage = %q, want %q", got, "curlimages/curl:8.9.0")
	}
	if got := m.ArtifactPath(); got != "/data/models/iris-mlp.onnx" {
		t.Fatalf("ArtifactPath = %q, want %q", got, "/data/models/iris-mlp.onnx")
	}
}

// TestModelSourceFileName pins the file-name extraction:
// query strings and fragments (signed URLs) are stripped, a
// URI ending in a slash falls back to model.bin, and a nil
// source is safe to call.
func TestModelSourceFileName(t *testing.T) {
	cases := []struct {
		uri  string
		want string
	}{
		{"https://artifacts.example.com/models/iris-mlp.onnx", "iris-mlp.onnx"},
		{"https://artifacts.example.com/models/iris-mlp.onnx?token=abc&expires=99", "iris-mlp.onnx"},
		{"https://artifacts.example.com/models/iris-mlp.onnx#v2", "iris-mlp.onnx"},
		{"https://artifacts.example.com/models/", "model.bin"},
		{"https://artifacts.example.com", "model.bin"},
	}
	for _, c := range cases {
		m := &ModelSourceSpec{URI: c.uri}
		if got := m.ArtifactFileName(); got != c.want {
			t.Fatalf("ArtifactFileName(%q) = %q, want %q", c.uri, got, c.want)
		}
	}
	var nilSource *ModelSourceSpec
	if got := nilSource.ArtifactFileName(); got != "model.bin" {
		t.Fatalf("nil ArtifactFileName = %q, want %q", got, "model.bin")
	}
	if got := nilSource.ArtifactPath(); got != "" {
		t.Fatalf("nil ArtifactPath = %q, want %q", got, "")
	}
	if got := nilSource.Describe(); got != "" {
		t.Fatalf("nil Describe = %q, want empty", got)
	}
}

// TestModelSourceDescribe pins the status line: the URI, the
// artifact path, and whether the download is checksum-verified
// (only the first 12 hex digits are shown).
func TestModelSourceDescribe(t *testing.T) {
	verified := &ModelSourceSpec{
		URI:    "https://artifacts.example.com/models/iris-mlp.onnx",
		SHA256: "9f2c4a1b8e77d0a3c5f6b2e1d4a9c8b7f6e5d4c3b2a1908f7e6d5c4b3a29180",
	}
	want := "https://artifacts.example.com/models/iris-mlp.onnx -> /models/iris-mlp.onnx (sha256: 9f2c4a1b8e77… verified)"
	if got := verified.Describe(); got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	unverified := &ModelSourceSpec{URI: "https://artifacts.example.com/models/iris-mlp.onnx"}
	want = "https://artifacts.example.com/models/iris-mlp.onnx -> /models/iris-mlp.onnx (unverified)"
	if got := unverified.Describe(); got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
}
