package crd

import (
	"strings"
)

// Defaults for the install bundle: the operator's own namespace and
// the container image it runs. The image is the versioned release
// image; a cluster install should pin a release tag, never :latest
// (the preflight Lint rule says the same about model images).
const (
	// DefaultNamespace is the namespace the operator installs into.
	DefaultNamespace = "ml-operator-system"
	// DefaultOperatorImage is the operator container image.
	DefaultOperatorImage = "ghcr.io/karnchoudhary-99/ml-operator:v0.1.0"
)

// InstallBundle returns the full install manifest for the operator
// as a multi-document YAML string: Namespace, ServiceAccount,
// ClusterRole, ClusterRoleBinding, and the operator Deployment.
// Empty namespace or image take the defaults; a namespace that is
// not a DNS-1123 subdomain is an error. The output is
// golden-tested against examples/install.yaml.
//
// The RBAC is the least privilege the reconciler needs today: the
// ModelDeployment CR and its status, the Deployments/Services/HPAs
// it renders, read-only pod access for readiness, and event
// creation. It does not grant secrets, nodes, or cluster-wide
// writes beyond those resources.
func InstallBundle(namespace, image string) (string, error) {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	if !validNamespace(namespace) {
		return "", installError("namespace %q must be a DNS-1123 subdomain", namespace)
	}
	if image == "" {
		image = DefaultOperatorImage
	}

	docs := []string{}
	add := func(doc *node) (string, error) {
		out, err := renderDoc(doc)
		if err != nil {
			return "", err
		}
		docs = append(docs, out)
		return out, nil
	}

	if _, err := add(namespaceDoc(namespace)); err != nil {
		return "", err
	}
	if _, err := add(serviceAccountDoc(namespace)); err != nil {
		return "", err
	}
	if _, err := add(clusterRoleDoc()); err != nil {
		return "", err
	}
	if _, err := add(clusterRoleBindingDoc(namespace)); err != nil {
		return "", err
	}
	if _, err := add(operatorDeploymentDoc(namespace, image)); err != nil {
		return "", err
	}
	return strings.Join(docs, "---\n"), nil
}

func namespaceDoc(namespace string) *node {
	return mapping(
		field("apiVersion", str("v1")),
		field("kind", str("Namespace")),
		field("metadata", mapping(field("name", str(namespace)))),
	)
}

func serviceAccountDoc(namespace string) *node {
	return mapping(
		field("apiVersion", str("v1")),
		field("kind", str("ServiceAccount")),
		field("metadata", mapping(
			field("name", str("ml-operator")),
			field("namespace", str(namespace)),
		)),
	)
}

// rule builds one ClusterRole policy rule.
func rule(groups []string, resources []string, verbs []string) *node {
	toList := func(vs []string) *node {
		items := make([]*node, 0, len(vs))
		for _, v := range vs {
			items = append(items, str(v))
		}
		return list(items...)
	}
	return mapping(
		field("apiGroups", toList(groups)),
		field("resources", toList(resources)),
		field("verbs", toList(verbs)),
	)
}

func clusterRoleDoc() *node {
	return mapping(
		field("apiVersion", str("rbac.authorization.k8s.io/v1")),
		field("kind", str("ClusterRole")),
		field("metadata", mapping(field("name", str("ml-operator")))),
		field("rules", list(
			rule([]string{Group},
				[]string{Plural},
				[]string{"get", "list", "watch", "create", "update", "patch", "delete"}),
			rule([]string{Group},
				[]string{Plural + "/status"},
				[]string{"get", "update", "patch"}),
			rule([]string{"apps"},
				[]string{"deployments"},
				[]string{"get", "list", "watch", "create", "update", "patch", "delete"}),
			rule([]string{""},
				[]string{"services"},
				[]string{"get", "list", "watch", "create", "update", "patch", "delete"}),
			rule([]string{""},
				[]string{"pods"},
				[]string{"get", "list", "watch"}),
			rule([]string{"autoscaling"},
				[]string{"horizontalpodautoscalers"},
				[]string{"get", "list", "watch", "create", "update", "patch", "delete"}),
			rule([]string{""},
				[]string{"events"},
				[]string{"create", "patch"}),
		)),
	)
}

func clusterRoleBindingDoc(namespace string) *node {
	return mapping(
		field("apiVersion", str("rbac.authorization.k8s.io/v1")),
		field("kind", str("ClusterRoleBinding")),
		field("metadata", mapping(field("name", str("ml-operator")))),
		field("roleRef", mapping(
			field("apiGroup", str("rbac.authorization.k8s.io")),
			field("kind", str("ClusterRole")),
			field("name", str("ml-operator")),
		)),
		field("subjects", list(mapping(
			field("kind", str("ServiceAccount")),
			field("name", str("ml-operator")),
			field("namespace", str(namespace)),
		))),
	)
}

func operatorDeploymentDoc(namespace, image string) *node {
	return mapping(
		field("apiVersion", str("apps/v1")),
		field("kind", str("Deployment")),
		field("metadata", mapping(
			field("name", str("ml-operator")),
			field("namespace", str(namespace)),
			field("labels", mapping(field("app", str("ml-operator")))),
		)),
		field("spec", mapping(
			field("replicas", integer(1)),
			field("selector", mapping(
				field("matchLabels", mapping(field("app", str("ml-operator")))),
			)),
			field("template", mapping(
				field("metadata", mapping(
					field("labels", mapping(field("app", str("ml-operator")))),
				)),
				field("spec", mapping(
					field("serviceAccountName", str("ml-operator")),
					field("containers", list(mapping(
						field("name", str("operator")),
						field("image", str(image)),
						field("args", list(
							str("--namespace=default"),
							str("--metrics-addr=:8080"),
							str("--reconcile-interval=30s"),
						)),
						field("ports", list(mapping(
							field("name", str("metrics")),
							field("containerPort", integer(8080)),
						))),
						field("resources", mapping(
							field("requests", mapping(
								field("cpu", str("100m")),
								field("memory", str("128Mi")),
							)),
							field("limits", mapping(
								field("cpu", str("500m")),
								field("memory", str("512Mi")),
							)),
						)),
						field("livenessProbe", mapping(
							field("httpGet", mapping(
								field("path", str("/healthz")),
								field("port", integer(8080)),
							)),
							field("initialDelaySeconds", integer(5)),
							field("periodSeconds", integer(10)),
						)),
					))),
				)),
			)),
		)),
	)
}
