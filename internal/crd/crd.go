package crd

import (
	"fmt"
	"regexp"
)

// The API group and version of the ModelDeployment custom resource.
// The group is a placeholder (ml.example.com) until the project
// registers a real domain; changing it later is a breaking change
// for every installed CRD, so it is a constant here, not a flag.
const (
	// Group is the API group of the custom resource.
	Group = "ml.example.com"
	// Version is the served and stored API version.
	Version = "v1alpha1"
	// Kind is the custom resource kind.
	Kind = "ModelDeployment"
	// Plural is the resource plural, used in the CRD name and in
	// kubectl paths (modeldeployments.ml.example.com).
	Plural = "modeldeployments"
	// Singular is the singular resource name.
	Singular = "modeldeployment"
	// ShortName is the kubectl short name (kubectl get mldep).
	ShortName = "mldep"
)

// CRDName is the metadata.name of the CustomResourceDefinition:
// "<plural>.<group>", as Kubernetes requires.
func CRDName() string { return Plural + "." + Group }

// CRD returns the CustomResourceDefinition for ModelDeployment as a
// YAML document (apiextensions.k8s.io/v1), with the openAPIV3
// schema from SpecSchema/StatusSchema, the status subresource
// enabled, and printer columns for kubectl (Phase, Ready, Age).
// The output is golden-tested against examples/crd.yaml.
func CRD() (string, error) {
	spec := mapping(
		field("type", str("object")),
		field("properties", mapping(
			field("apiVersion", strSchema("").toNode()),
			field("kind", strSchema("").toNode()),
			field("metadata", objSchema("", nil).toNode()),
			field("spec", SpecSchema().toNode()),
			field("status", StatusSchema().toNode()),
		)),
	)
	doc := mapping(
		field("apiVersion", str("apiextensions.k8s.io/v1")),
		field("kind", str("CustomResourceDefinition")),
		field("metadata", mapping(
			field("name", str(CRDName())),
		)),
		field("spec", mapping(
			field("group", str(Group)),
			field("names", mapping(
				field("kind", str(Kind)),
				field("listKind", str(Kind+"List")),
				field("plural", str(Plural)),
				field("singular", str(Singular)),
				field("shortNames", list(str(ShortName))),
			)),
			field("scope", str("Namespaced")),
			field("versions", list(mapping(
				field("name", str(Version)),
				field("served", boolean(true)),
				field("storage", boolean(true)),
				field("subresources", mapping(
					field("status", mapping()),
				)),
				field("additionalPrinterColumns", list(
					mapping(
						field("name", str("Phase")),
						field("type", str("string")),
						field("jsonPath", str(".status.phase")),
					),
					mapping(
						field("name", str("Ready")),
						field("type", str("string")),
						field("jsonPath", str(".status.readyReplicas")),
					),
					mapping(
						field("name", str("Age")),
						field("type", str("date")),
						field("jsonPath", str(".metadata.creationTimestamp")),
					),
				)),
				field("schema", mapping(
					field("openAPIV3Schema", spec),
				)),
			))),
		)),
	)
	return renderDoc(doc)
}

var dns1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// validNamespace reports whether ns is a usable install namespace
// (a DNS-1123 subdomain, the same rule api.Validate applies to
// deployment namespaces).
func validNamespace(ns string) bool { return ns != "" && dns1123Re.MatchString(ns) }

// installError is a usage error from InstallBundle.
func installError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
