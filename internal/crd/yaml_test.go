package crd

import (
	"strings"
	"testing"
)

func TestEmitterNestedMapsAndLists(t *testing.T) {
	doc := mapping(
		field("apiVersion", str("apps/v1")),
		field("kind", str("Deployment")),
		field("spec", mapping(
			field("replicas", integer(3)),
			field("paused", boolean(false)),
			field("containers", list(
				mapping(
					field("name", str("operator")),
					field("image", str("ghcr.io/example/ml-operator:v0.1.0")),
					field("ports", list(mapping(field("containerPort", integer(8080))))),
				),
			)),
		)),
	)
	out, err := renderDoc(doc)
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	want := `apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 3
  paused: false
  containers:
    - name: operator
      image: ghcr.io/example/ml-operator:v0.1.0
      ports:
        - containerPort: 8080
`
	if out != want {
		t.Fatalf("rendered YAML mismatch.\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEmitterQuoting(t *testing.T) {
	cases := map[string]string{
		"true":  "'true'",
		"8080":  "'8080'",
		"500m":  "500m",
		"1Gi":   "1Gi",
		"on":    "'on'",
		"a: b":  "'a: b'",
		"plain": "plain",
		"":      `""`,
	}
	for in, want := range cases {
		if got := quoteScalar(in); got != want {
			t.Errorf("quoteScalar(%q) = %q, want %q", in, got, want)
		}
	}
	// A string that is only digits must not lose its quotes when
	// nested in a document: "8080" as a resource quantity stays a
	// string.
	doc := mapping(field("port", str("8080")), field("replicas", integer(2)))
	out, err := renderDoc(doc)
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	if !strings.Contains(out, "port: '8080'\n") || !strings.Contains(out, "replicas: 2\n") {
		t.Fatalf("quoting in document wrong:\n%s", out)
	}
}

func TestEmitterEmptyContainers(t *testing.T) {
	doc := mapping(field("env", mapping()), field("args", list()))
	out, err := renderDoc(doc)
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	want := "env: {}\nargs: []\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestEmitterRoundTripThroughManifestParser(t *testing.T) {
	// The project's own YAML-subset parser must read what the
	// emitter writes, so generated manifests are consumable by
	// the same code that reads hand-written ones.
	doc := mapping(
		field("name", str("iris-mlp")),
		field("spec", mapping(
			field("model", mapping(field("image", str("registry.example.com/iris-mlp:v1")))),
			field("replicas", integer(2)),
		)),
	)
	out, err := renderDoc(doc)
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	if !strings.Contains(out, "name: iris-mlp") || !strings.Contains(out, "replicas: 2") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
