package crd

import (
	"strings"
	"testing"
)

func findProp(s *schema, name string) *schema {
	for _, p := range s.properties {
		if p.name == name {
			return p.s
		}
	}
	return nil
}

func TestSpecSchemaMirrorsValidation(t *testing.T) {
	spec := SpecSchema()

	// Required fields: the model block, and the image inside it.
	if len(spec.required) != 1 || spec.required[0] != "model" {
		t.Errorf("spec required = %v, want [model]", spec.required)
	}
	model := findProp(spec, "model")
	if model == nil {
		t.Fatal("spec.model missing from schema")
	}
	if len(model.required) != 1 || model.required[0] != "image" {
		t.Errorf("spec.model required = %v, want [image]", model.required)
	}

	// Every range below mirrors a check in api.Validate.
	rangeCases := []struct {
		path     string
		s        *schema
		min, max float64
		maxSet   bool
	}{
		{"port", findProp(spec, "port"), 1, 65535, true},
		{"replicas", findProp(spec, "replicas"), 0, 0, false},
		{"canary.weight", findProp(findProp(spec, "canary"), "weight"), 1, 99, true},
		{"canary.gate.maxErrorRate", findProp(findProp(findProp(spec, "canary"), "gate"), "maxErrorRate"), 0, 1, true},
		{"canary.gate.minReadyRatio", findProp(findProp(findProp(spec, "canary"), "gate"), "minReadyRatio"), 0, 1, true},
		{"autoscaling.maxReplicas", findProp(findProp(spec, "autoscaling"), "maxReplicas"), 1, 0, false},
		{"autoscaling.targetCPUUtilization", findProp(findProp(spec, "autoscaling"), "targetCPUUtilization"), 0, 100, true},
		{"availability.minAvailable", findProp(findProp(spec, "availability"), "minAvailable"), 0, 0, false},
		{"availability.maxSkew", findProp(findProp(spec, "availability"), "maxSkew"), 0, 10, true},
	}
	for _, c := range rangeCases {
		if c.s == nil {
			t.Errorf("spec.%s missing from schema", c.path)
			continue
		}
		if c.s.minimum == nil || *c.s.minimum != c.min {
			t.Errorf("spec.%s minimum = %v, want %v", c.path, c.s.minimum, c.min)
		}
		if c.maxSet {
			if c.s.maximum == nil || *c.s.maximum != c.max {
				t.Errorf("spec.%s maximum = %v, want %v", c.path, c.s.maximum, c.max)
			}
		} else if c.s.maximum != nil {
			t.Errorf("spec.%s maximum = %v, want unset", c.path, *c.s.maximum)
		}
	}

	strategy := findProp(spec, "strategy")
	wantEnum := []string{"Recreate", "RollingUpdate", "Canary", "BlueGreen"}
	if len(strategy.enum) != len(wantEnum) {
		t.Fatalf("strategy enum = %v, want %v", strategy.enum, wantEnum)
	}
	for i, e := range wantEnum {
		if strategy.enum[i] != e {
			t.Errorf("strategy enum[%d] = %q, want %q", i, strategy.enum[i], e)
		}
	}

	canary := findProp(spec, "canary")
	if len(canary.required) != 1 || canary.required[0] != "weight" {
		t.Errorf("canary required = %v, want [weight]", canary.required)
	}

	env := findProp(spec, "env")
	if env.additionalProperties == nil || env.additionalProperties.typ != "string" {
		t.Error("spec.env must allow arbitrary string values (additionalProperties: type string)")
	}

	// Availability: the block exists, anti-affinity is a boolean,
	// and nothing in it is required (absent means the defaults).
	avail := findProp(spec, "availability")
	if avail == nil {
		t.Fatal("spec.availability missing from schema")
	}
	anti := findProp(avail, "antiAffinity")
	if anti == nil || anti.typ != "boolean" {
		t.Error("spec.availability.antiAffinity missing or not a boolean")
	}
	if len(avail.required) != 0 {
		t.Errorf("availability required = %v, want none (absent means defaults)", avail.required)
	}
}

func TestStatusSchema(t *testing.T) {
	status := StatusSchema()
	phase := findProp(status, "phase")
	if phase == nil {
		t.Fatal("status.phase missing from schema")
	}
	want := []string{"Pending", "Progressing", "Available", "Degraded", "Failed"}
	if len(phase.enum) != len(want) {
		t.Fatalf("phase enum = %v, want %v", phase.enum, want)
	}
	for i, e := range want {
		if phase.enum[i] != e {
			t.Errorf("phase enum[%d] = %q, want %q", i, phase.enum[i], e)
		}
	}
	conditions := findProp(status, "conditions")
	if conditions == nil || conditions.items == nil {
		t.Fatal("status.conditions must be an array with an item schema")
	}
	condStatus := findProp(conditions.items, "status")
	if condStatus == nil {
		t.Fatal("conditions[].status missing from schema")
	}
	if len(condStatus.enum) != 3 || condStatus.enum[0] != "True" {
		t.Errorf("condition status enum = %v, want [True False Unknown]", condStatus.enum)
	}
	condTime := findProp(conditions.items, "lastTransitionTime")
	if condTime == nil || condTime.format != "date-time" {
		t.Errorf("conditions[].lastTransitionTime format = %q, want date-time", condTime.format)
	}
}

func TestSchemaRendersToYAML(t *testing.T) {
	out, err := renderDoc(SpecSchema().toNode())
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	for _, want := range []string{
		"type: object",
		"model:",
		"required:\n  - model",
		"port:",
		"minimum: 1",
		"maximum: 65535",
		"weight:",
		"minimum: 1",
		"maximum: 99",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("schema YAML missing %q:\n%s", want, out)
		}
	}
}
