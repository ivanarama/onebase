package cli

import (
	"reflect"
	"testing"
)

func TestSchemaProcessorParamCoversModelKeys(t *testing.T) {
	keys := yamlTagsOfStruct(t, "../processor/processor.go", "Param")
	props := schemaAt(t, publishedParamSchema(t, "processor"), "properties")
	known := make(map[string]bool, len(keys))
	for _, key := range keys {
		known[key] = true
		if _, ok := props[key]; !ok {
			t.Errorf("processor parameter schema does not describe YAML key %q", key)
		}
	}
	for key := range props {
		if !known[key] {
			t.Errorf("schema promises key %q that processor.Param does not read", key)
		}
	}
}

func TestSchemaProcessorParamLabelsAndDefault(t *testing.T) {
	param := publishedParamSchema(t, "processor")
	aggregate := schemaAt(t, publishedSchema(t), "$defs", "processor", "properties", "params", "items")
	if !reflect.DeepEqual(param, aggregate) {
		t.Error("separate and aggregate processor parameter schemas differ")
	}
	for name, schema := range map[string]map[string]any{"processor": param, "aggregate": aggregate} {
		t.Run(name, func(t *testing.T) {
			props := schemaAt(t, schema, "properties")
			labels := schemaAt(t, props, "labels")
			if labels["type"] != "object" {
				t.Errorf("labels.type = %#v, want object", labels["type"])
			}
			if values := schemaAt(t, labels, "additionalProperties"); values["type"] != "string" {
				t.Errorf("labels values type = %#v, want string", values["type"])
			}
			// processor.Param.Default is any: the schema must accept every JSON
			// value, including null and composite values. Only an annotation is
			// permitted here, with no validation keywords restricting the value.
			defaults := schemaAt(t, props, "default")
			for key := range defaults {
				if key != "description" {
					t.Errorf("default must be unconstrained, found keyword %q", key)
				}
			}
			if schema["additionalProperties"] != false {
				t.Error("processor parameters must still reject unknown keys")
			}
			if !reflect.DeepEqual(schema["required"], []any{"name", "type"}) {
				t.Errorf("required = %#v, want name and type only", schema["required"])
			}
			if required := schemaAt(t, props, "required"); required["type"] != "boolean" {
				t.Errorf("required.type = %#v, want boolean", required["type"])
			}
			if _, ok := props["period_end"]; ok {
				t.Error("report-only period_end leaked into processor parameters")
			}
		})
	}
	if _, ok := schemaAt(t, publishedParamSchema(t, "report"), "properties")["required"]; ok {
		t.Error("processor-only required leaked into report parameters")
	}
}
