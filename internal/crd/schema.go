package crd

// schema is one node of an openAPIV3 validation schema, in the shape
// apiextensions-apiserver expects under
// versions[].schema.openAPIV3Schema. It is deliberately a small
// hand-rolled tree (not a generic JSON-Schema library): the CRD
// schema mirrors internal/api.Validate field for field, so a spec
// the API server admits is one the operator accepts.
//
// Cross-field rules the schema language cannot express (autoscaling
// minReplicas <= maxReplicas, "at least one scaling target", the
// canary-gate "zero means default" rule) are enforced by
// api.Validate and noted in the field descriptions — the schema is
// the first gate, the operator's validation is the second.
type schema struct {
	typ                  string
	format               string
	description          string
	enum                 []string
	minimum              *float64
	maximum              *float64
	required             []string
	properties           []schemaProp
	items                *schema
	additionalProperties *schema
}

type schemaProp struct {
	name string
	s    *schema
}

// strSchema is a plain string field.
func strSchema(desc string) *schema { return &schema{typ: "string", description: desc} }

// boolSchema is a plain boolean field.
func boolSchema(desc string) *schema { return &schema{typ: "boolean", description: desc} }

// intSchema is an integer field with an optional range; a nil bound
// means unbounded on that side.
func intSchema(desc string, min, max *int64) *schema {
	s := &schema{typ: "integer", description: desc}
	if min != nil {
		f := float64(*min)
		s.minimum = &f
	}
	if max != nil {
		f := float64(*max)
		s.maximum = &f
	}
	return s
}

// numSchema is a floating-point field with an optional range.
func numSchema(desc string, min, max *float64) *schema {
	return &schema{typ: "number", description: desc, minimum: min, maximum: max}
}

// objSchema is an object field with ordered properties. required
// names the properties that must be present.
func objSchema(desc string, required []string, props ...schemaProp) *schema {
	return &schema{typ: "object", description: desc, required: required, properties: props}
}

// prop builds one object property.
func prop(name string, s *schema) schemaProp { return schemaProp{name: name, s: s} }

// arrSchema is an array field with one item schema.
func arrSchema(desc string, items *schema) *schema {
	return &schema{typ: "array", description: desc, items: items}
}

// intPtr is a helper for intSchema bounds.
func intPtr(v int64) *int64 { return &v }

// floatPtr is a helper for numSchema bounds.
func floatPtr(v float64) *float64 { return &v }

// toNode renders the schema as a YAML node for the CRD document.
func (s *schema) toNode() *node {
	pairs := []pair{field("type", str(s.typ))}
	if s.format != "" {
		pairs = append(pairs, field("format", str(s.format)))
	}
	if s.description != "" {
		pairs = append(pairs, field("description", str(s.description)))
	}
	if len(s.enum) > 0 {
		items := make([]*node, 0, len(s.enum))
		for _, e := range s.enum {
			items = append(items, str(e))
		}
		pairs = append(pairs, field("enum", list(items...)))
	}
	// Integer bounds render as integers: a float rendering of a
	// large bound (4294967295 -> "4.294967295e+09") is valid YAML
	// but unreadable, and a CRD is a document humans read.
	// Number bounds keep the float rendering.
	if s.minimum != nil {
		if s.typ == "integer" {
			pairs = append(pairs, field("minimum", integer(int64(*s.minimum))))
		} else {
			pairs = append(pairs, field("minimum", number(*s.minimum)))
		}
	}
	if s.maximum != nil {
		if s.typ == "integer" {
			pairs = append(pairs, field("maximum", integer(int64(*s.maximum))))
		} else {
			pairs = append(pairs, field("maximum", number(*s.maximum)))
		}
	}
	if len(s.required) > 0 {
		items := make([]*node, 0, len(s.required))
		for _, r := range s.required {
			items = append(items, str(r))
		}
		pairs = append(pairs, field("required", list(items...)))
	}
	if len(s.properties) > 0 {
		props := make([]pair, 0, len(s.properties))
		for _, p := range s.properties {
			props = append(props, field(p.name, p.s.toNode()))
		}
		pairs = append(pairs, field("properties", mapping(props...)))
	}
	if s.items != nil {
		pairs = append(pairs, field("items", s.items.toNode()))
	}
	if s.additionalProperties != nil {
		pairs = append(pairs, field("additionalProperties", s.additionalProperties.toNode()))
	}
	return mapping(pairs...)
}

// gateSchema is the shared health-gate threshold object, used by
// canary and blue-green rollouts. A zero field means "operator
// default" (5% errors, 500ms p95, every pod ready) — it never
// disables its check.
func gateSchema() *schema {
	return objSchema("Health-gate thresholds for this rollout. A zero field takes the operator default; it never disables its check.",
		nil,
		prop("maxErrorRate", numSchema("Maximum tolerated error fraction (0-1). 0 means the default (5%).", floatPtr(0), floatPtr(1))),
		prop("maxP95LatencyMs", numSchema("Maximum tolerated p95 latency in milliseconds. 0 means the default (500ms).", floatPtr(0), nil)),
		prop("minReadyRatio", numSchema("Minimum ready/desired replicas (0-1). 0 means the default (1.0, every pod ready).", floatPtr(0), floatPtr(1))),
	)
}

// SpecSchema returns the openAPIV3 schema for
// ModelDeploymentSpec. Every range mirrors api.Validate; the
// descriptions name the default where one exists.
func SpecSchema() *schema {
	return objSchema("Desired state of a deployed model.",
		[]string{"model"},
		prop("model", objSchema("The model artifact to serve.",
			[]string{"image"},
			prop("image", strSchema("Container image serving the model, e.g. \"registry.example.com/iris-mlp:v2\".")),
			prop("framework", strSchema("Serving runtime (e.g. \"onnx\", \"torchserve\", \"triton\", \"custom\"). Only used for the default readiness path when health.path is empty.")),
			prop("version", strSchema("Model version label, recorded in status and metrics.")),
		)),
		prop("replicas", intSchema("Desired pod count. Empty means the default (1). Autoscaling, when enabled, overrides this at runtime.", intPtr(0), nil)),
		prop("resources", objSchema("Per-pod CPU/memory requests and limits, as Kubernetes quantities (\"500m\", \"1Gi\").",
			nil,
			prop("cpuRequest", strSchema("CPU request, e.g. \"500m\".")),
			prop("cpuLimit", strSchema("CPU limit, e.g. \"2\". Must be >= cpuRequest.")),
			prop("memoryRequest", strSchema("Memory request, e.g. \"512Mi\".")),
			prop("memoryLimit", strSchema("Memory limit, e.g. \"1Gi\". Must be >= memoryRequest.")),
		)),
		prop("port", intSchema("Container port the model server listens on. Default 8080.", intPtr(1), intPtr(65535))),
		prop("strategy", &schema{
			typ:         "string",
			description: "How a new model revision replaces the old one. Default RollingUpdate.",
			enum:        []string{"Recreate", "RollingUpdate", "Canary", "BlueGreen"},
		}),
		prop("canary", objSchema("Canary rollout configuration. Required when strategy is Canary, ignored otherwise.",
			[]string{"weight"},
			prop("weight", intSchema("Initial percentage of traffic sent to the new revision (1-99). The operator steps it up after each health gate passes.", intPtr(1), intPtr(99))),
			prop("steps", intSchema("Number of traffic increments to reach 100%. 0 means a single step (weight straight to 100% after the gate).", intPtr(0), nil)),
			prop("intervalSeconds", intSchema("Pause between steps, in seconds.", intPtr(0), nil)),
			prop("gate", gateSchema()),
		)),
		prop("blueGreen", objSchema("Blue-green rollout configuration. Optional when strategy is BlueGreen (absent means the operator defaults); ignored otherwise.",
			nil,
			prop("gate", gateSchema()),
		)),
		prop("autoscaling", objSchema("Horizontal autoscaling for this model. minReplicas must be <= maxReplicas, and at least one of targetCPUUtilization / targetRequestsPerSecond must be set — enforced by the operator, not the schema.",
			nil,
			prop("minReplicas", intSchema("Floor. 0 means unset (defaults to 1).", intPtr(0), nil)),
			prop("maxReplicas", intSchema("Ceiling; must be >= minReplicas.", intPtr(1), nil)),
			prop("targetCPUUtilization", intSchema("Average CPU percentage that triggers scaling (1-100). 0 disables CPU-based scaling.", intPtr(0), intPtr(100))),
			prop("targetRequestsPerSecond", intSchema("Per-pod request rate that triggers scaling. 0 disables RPS-based scaling.", intPtr(0), nil)),
		)),
		prop("availability", objSchema("How the operator keeps the model serving through voluntary disruptions and spreads its pods across nodes. Absent means the operator defaults (anti-affinity on, maxSkew 1, minAvailable replicas-1). minAvailable must be < replicas — enforced by the operator, not the schema.",
			nil,
			prop("minAvailable", intSchema("Pods that must stay available during a voluntary disruption. 0 means the default (replicas-1).", intPtr(0), nil)),
			prop("antiAffinity", boolSchema("Keep pods of one deployment off the same node. Absent means the default (true).")),
			prop("maxSkew", intSchema("Topology-spread maxSkew: the most pods any one node may hold above the least-loaded node. 0 means the default (1).", intPtr(0), intPtr(10))),
		)),
		prop("security", objSchema("Pod- and container-level security context for the model's serving pods. Absent means the operator defaults (non-root uid 10001, read-only root filesystem, no privilege escalation, all capabilities dropped, RuntimeDefault seccomp). A uid of 0 with runAsNonRoot on is a contradiction — enforced by the operator, not the schema.",
			nil,
			prop("runAsNonRoot", boolSchema("Refuse to start the pod if any container would run as uid 0. Absent means the default (true).")),
			prop("runAsUser", intSchema("Uid every container runs as. Absent means the default (10001). 0 is only legal with runAsNonRoot false.", intPtr(0), intPtr(4294967295))),
			prop("readOnlyRootFilesystem", boolSchema("Mount the container's root filesystem read-only. Absent means the default (true).")),
			prop("allowPrivilegeEscalation", boolSchema("Let a process gain more privileges than its parent. Absent means the default (false).")),
			prop("dropCapabilities", arrSchema("Linux capabilities removed from the container's bounding set. Absent or empty means the default ([\"ALL\"]).", strSchema("One capability name, e.g. \"ALL\" or \"NET_BIND_SERVICE\"."))),
			prop("seccompProfile", strSchema("Seccomp profile for the pod: \"RuntimeDefault\", \"Unconfined\", or \"Localhost/<profile>\". Empty means the default (\"RuntimeDefault\").")),
		)),
		prop("placement", objSchema("Pin the model's pods to nodes: which nodes they may run on (nodeSelector) and which node taints they tolerate (tolerations). Absent means unconstrained: the scheduler places the pods anywhere. Cross-field rules the schema cannot express (nodeSelector keys must be label keys, a value is only legal with operator Equal, tolerationSeconds only with effect NoExecute) are enforced by the operator, not the schema.",
			nil,
			prop("nodeSelector", &schema{
				typ:                  "object",
				description:          "Node labels the pod's node must carry, e.g. {\"accelerator\": \"nvidia-tesla-t4\"}. Keys must be valid label keys, values valid label values.",
				additionalProperties: strSchema(""),
			}),
			prop("tolerations", arrSchema("Node taints the pods tolerate. Empty means the pods tolerate nothing: a tainted node rejects them.",
				objSchema("One toleration: a taint key to tolerate, how to match it, and for which effect.",
					nil,
					prop("key", strSchema("Taint key to tolerate. Empty is only legal with operator Exists, where it matches every taint.")),
					prop("operator", &schema{
						typ:         "string",
						description: "How to match the taint: \"Equal\" (key and value must both match) or \"Exists\" (the key alone must exist; value must be empty). Empty means \"Equal\".",
						enum:        []string{"Equal", "Exists"},
					}),
					prop("value", strSchema("Taint value to tolerate. Must be empty when operator is Exists.")),
					prop("effect", &schema{
						typ:         "string",
						description: "Taint effect to tolerate: \"NoSchedule\", \"PreferNoSchedule\", \"NoExecute\", or empty (any effect).",
						enum:        []string{"NoSchedule", "PreferNoSchedule", "NoExecute"},
					}),
					prop("tolerationSeconds", intSchema("Seconds the pod stays bound after the matching taint appears. Only meaningful with effect NoExecute; empty means evict immediately.", intPtr(0), nil)),
				),
			)),
		)),
		prop("modelSource", objSchema("Fetch the model artifact into the pod at startup: an init container downloads it from uri into a shared volume mounted at mountPath, instead of baking the weights into the serving image. Absent means the image already contains the model. Only http(s) URIs are supported — artifact stores that need credentials (s3://) are rejected by the operator, not the schema. A checksum, when set, must be 64 hex characters; an empty checksum means the download is unverified (the operator warns about it).",
			[]string{"uri"},
			prop("uri", strSchema("http(s) URL of the model artifact, e.g. \"https://artifacts.example.com/models/iris-mlp.onnx\". Required.")),
			prop("sha256", strSchema("Expected SHA-256 hex digest of the downloaded artifact. Empty means the download is not verified.")),
			prop("mountPath", strSchema("Where the shared volume mounts in both the init container and the serving container. Empty means the default (\"/models\"). Must be an absolute path.")),
			prop("downloaderImage", strSchema("Init-container image that fetches the artifact. Empty means the default (\"curlimages/curl:8.5.0\").")),
		)),
		prop("imagePullSecrets", arrSchema("Kubernetes secrets holding registry credentials for pulling the serving image (e.g. a docker-registry secret for a private registry). Empty means the pods pull without credentials. Each name must be a DNS-1123 subdomain — enforced by the operator, not the schema.",
			strSchema("One secret name, e.g. \"regcred\".")),
		),
		prop("labels", &schema{
			typ:                  "object",
			description:          "Extra labels stamped onto every object the operator renders for this model (Deployments incl. pod template, Service, HPA, PDB) — for the team's own selectors, cost reports and dashboards. Keys must be valid label keys and values at most 63 characters; keys under app.kubernetes.io/ and ml-operator/ are rejected — enforced by the operator, not the schema. Never touches the operator's selectors.",
			additionalProperties: strSchema(""),
		}),
		prop("annotations", &schema{
			typ:                  "object",
			description:          "Extra annotations stamped onto every object the operator renders for this model, next to the labels — free-form metadata for humans and tooling (owner, runbook URL). Keys must be valid annotation keys; keys under app.kubernetes.io/ and ml-operator/ are rejected — enforced by the operator, not the schema. Values are free-form strings, never selected on.",
			additionalProperties: strSchema(""),
		}),
		prop("env", &schema{
			typ:                  "object",
			description:          "Extra environment variables for the model container. Keys must match [A-Za-z_][A-Za-z0-9_]*.",
			additionalProperties: strSchema(""),
		}),
		prop("health", objSchema("Readiness gate for the model container.",
			nil,
			prop("path", strSchema("HTTP readiness endpoint, e.g. \"/healthz\". Must start with '/'. Empty takes the framework default.")),
			prop("initialDelaySeconds", intSchema("Wait after container start before the first probe.", intPtr(0), nil)),
			prop("periodSeconds", intSchema("Probe interval, in seconds.", intPtr(0), nil)),
		)),
		prop("paused", boolSchema("Suspend reconciliation while true: the operator makes no cluster changes, advances no rollout, and applies no autoscaling decision. Live objects keep serving as they are; resume by setting false. Default false.")),
		prop("maintenanceWindow", objSchema("Recurring weekly window, in UTC, during which the operator may change cluster objects for this deployment. Outside the window a reconcile that would change anything defers until the window opens; a reconcile with nothing to do still runs. Absent means unrestricted. Cross-field rules the schema cannot express (days must be weekday abbreviations, times \"HH:MM\", start != end) are enforced by the operator, not the schema.",
			nil,
			prop("days", arrSchema("Weekdays the window opens on, as three-letter abbreviations. Empty means every day.", &schema{typ: "string", enum: []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}})),
			prop("start", strSchema("Opening time, \"HH:MM\" in 24-hour UTC (e.g. \"02:00\").")),
			prop("end", strSchema("Closing time, \"HH:MM\" in 24-hour UTC. Earlier than start means the window runs overnight, past midnight into the next day.")),
		)),
	)
}

// StatusSchema returns the openAPIV3 schema for
// ModelDeploymentStatus. The status subresource is enabled on the
// CRD, so status is only ever written by the operator, never by a
// user applying a manifest.
func StatusSchema() *schema {
	return objSchema("Observed state of a deployed model.",
		nil,
		prop("phase", &schema{
			typ:         "string",
			description: "Lifecycle phase of the deployment.",
			enum:        []string{"Pending", "Progressing", "Available", "Degraded", "Failed"},
		}),
		prop("readyReplicas", intSchema("Number of pods passing the readiness gate.", intPtr(0), nil)),
		prop("observedGeneration", intSchema("Last spec generation the operator acted on.", intPtr(0), nil)),
		prop("message", strSchema("Human-readable summary of the current state.")),
		prop("previousImage", strSchema("Container image the stable Deployment served before the current one. Empty until the first image change; `mlop rollback` restores it.")),
		prop("previousVersion", strSchema("Model version label that went with previousImage.")),
		prop("conditions", arrSchema("Latest condition per type (Ready, Progressing, Available), following the Kubernetes condition conventions.",
			objSchema("One status condition.",
				[]string{"type", "status"},
				prop("type", strSchema("Condition type, e.g. \"Ready\".")),
				prop("status", &schema{
					typ:         "string",
					description: "Condition status.",
					enum:        []string{"True", "False", "Unknown"},
				}),
				prop("reason", strSchema("Short machine-readable reason for the current status.")),
				prop("message", strSchema("Human-readable detail.")),
				prop("lastTransitionTime", &schema{
					typ:         "string",
					format:      "date-time",
					description: "Last time the status value changed.",
				}),
			),
		)),
	)
}
