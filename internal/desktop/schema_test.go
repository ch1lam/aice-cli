package desktop

import (
	"bytes"
	"encoding/json"
	"testing"
)

func schemaFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var inventory schemaInventory
	if err := json.Unmarshal(macSchemaInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory.Tools
}

func TestReviewedSchemaContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tool string
		change     func(map[string]any)
	}{
		{"missing parameter", "click", func(s map[string]any) { delete(s["properties"].(map[string]any), "capture_id") }},
		{"parameter type", "type_text", func(s map[string]any) { schemaProperty(s, "text")["type"] = "number" }},
		{"delivery mode", "drag", func(s map[string]any) { schemaProperty(s, "delivery_mode")["enum"] = []string{"foreground"} }},
		{"permission default", "check_permissions", func(s map[string]any) { schemaProperty(s, "prompt")["default"] = true }},
		{"required argument", "set_value", func(s map[string]any) { s["required"] = []string{"value"} }},
		{"duration bound", "drag", func(s map[string]any) { schemaProperty(s, "duration_ms")["maximum"] = 20000 }},
		{"minimum bound", "scroll", func(s map[string]any) { schemaProperty(s, "amount")["minimum"] = 0 }},
		{"target alternatives", "click", func(s map[string]any) { schemaProperty(s, "target")["oneOf"] = []any{} }},
		{"new required field", "start_session", func(s map[string]any) { s["required"] = []string{"new_field"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			schemas := schemaFixture(t)
			var schema map[string]any
			if err := json.Unmarshal(schemas[test.tool], &schema); err != nil {
				t.Fatal(err)
			}
			test.change(schema)
			schemas[test.tool], _ = json.Marshal(schema)
			if _, err := reviewedMacTools(schemas); !serviceHasCode(err, "incompatible_service") {
				t.Fatalf("contract drift accepted: %v", err)
			}
		})
	}
}

func schemaProperty(schema map[string]any, name string) map[string]any {
	return schema["properties"].(map[string]any)[name].(map[string]any)
}

func TestReviewedSchemasIgnoreFormattingAndExtraTools(t *testing.T) {
	t.Parallel()
	schemas := schemaFixture(t)
	for name, schema := range schemas {
		var compact bytes.Buffer
		if err := json.Compact(&compact, schema); err != nil {
			t.Fatal(err)
		}
		schemas[name] = compact.Bytes()
	}
	schemas["unreviewed_tool"] = json.RawMessage(`{"type":"object"}`)
	reviewed, err := reviewedMacTools(schemas)
	if err != nil || len(reviewed) != 15 || reviewed["unreviewed_tool"] != nil {
		t.Fatalf("unexpected admitted tools: %d, %v", len(reviewed), err)
	}
}
