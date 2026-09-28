package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func resultDetails() *ToolResultDetails {
	return &ToolResultDetails{State: ExecutionReturned,
		Binding:           &ToolBinding{Source: "user", ServiceID: "fixture", ConnectionFingerprint: "connection-hash", ToolName: "inspect", SchemaFingerprint: "schema-hash"},
		StructuredContent: json.RawMessage(`{"count":9007199254740993,"label":"结果"}`),
	}
}

func TestToolResultDetailsOwnershipAndReplay(t *testing.T) {
	t.Parallel()
	source := ToolResult{CallID: "call", Details: resultDetails(), IsError: true, Content: []ContentPart{
		NewTextContent("before").Part(), {Type: ContentTypeImage, Image: &ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, NewTextContent("after").Part(),
	}}
	message, err := NewToolResultMessage(source)
	if err != nil {
		t.Fatal(err)
	}
	source.Details.StructuredContent[0] = '!'
	source.Details.Binding.ServiceID = "changed"
	source.Content[1].Image.Data[0] = '!'
	if !json.Valid(message.Details.StructuredContent) || message.Details.Binding.ServiceID != "fixture" || string(message.Content[1].Image.Data) != "image" {
		t.Fatal("constructor aliases source")
	}
	copied, err := CloneAgentMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	copied.(ToolResultMessage).Details.StructuredContent[0] = '!'
	copied.(ToolResultMessage).Details.Binding.ToolName = "changed"
	if !json.Valid(message.Details.StructuredContent) || message.Details.Binding.ToolName != "inspect" {
		t.Fatal("clone aliases source")
	}
	encoded, err := MarshalAgentMessages([]AgentMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalAgentMessages(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(message, restored[0]) {
		t.Fatalf("round trip lost details: %s", encoded)
	}
	nested := cloneContentParts([]ContentPart{{Type: ContentTypeToolResult, ToolResult: &ToolResult{CallID: "call", Details: message.Details}}})
	nested[0].ToolResult.Details.StructuredContent[0] = '!'
	if !json.Valid(message.Details.StructuredContent) {
		t.Fatal("nested clone aliases source")
	}
	legacy, err := UnmarshalAgentMessages([]byte(`[{"role":"toolResult","tool_call_id":"old","content":[],"timestamp":1}]`))
	if err != nil || legacy[0].(ToolResultMessage).Details != nil {
		t.Fatalf("legacy replay: %v", err)
	}
}

func TestToolResultDetailsValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*ToolResultDetails)
	}{
		{"empty state", func(d *ToolResultDetails) { d.State = "" }},
		{"unknown state", func(d *ToolResultDetails) { d.State = "success" }},
		{"broken json", func(d *ToolResultDetails) { d.StructuredContent = json.RawMessage(`{`) }},
		{"large json", func(d *ToolResultDetails) {
			d.StructuredContent = json.RawMessage(`"` + strings.Repeat("x", MaxStructuredResultBytes) + `"`)
		}},
		{"invalid UTF-8", func(d *ToolResultDetails) { d.StructuredContent = []byte{'"', 0xff, '"'} }},
		{"incomplete binding", func(d *ToolResultDetails) { d.Binding.ConnectionFingerprint = "" }},
		{"control in binding", func(d *ToolResultDetails) { d.Binding.ToolName = "bad\nname" }},
		{"large notice", func(d *ToolResultDetails) { d.Loss = strings.Repeat("x", 4097) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := resultDetails()
			tt.modify(d)
			if _, err := NewToolResultMessage(ToolResult{CallID: "call", Details: d}); err == nil {
				t.Fatal("invalid details accepted")
			}
		})
	}
	for _, state := range []ExecutionState{ExecutionNotDispatched, ExecutionReturned, ExecutionUnknown} {
		t.Run(string(state), func(t *testing.T) {
			if err := (&ToolResultDetails{State: state}).Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestToolResultProjectionPreservesOrderAndCountsJSON(t *testing.T) {
	t.Parallel()
	content := []ContentPart{NewTextContent("before").Part(), {Type: ContentTypeImage, Image: &ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, NewTextContent("after").Part()}
	d := resultDetails()
	d.State = ExecutionUnknown
	d.Loss = "response exceeded transport bound"
	projected := ToolResultModelContent(content, d, false)
	if !reflect.DeepEqual(projected[:3], content) || len(projected) != 6 {
		t.Fatalf("projection changed order: %#v", projected)
	}
	if !strings.Contains(projected[3].Text, "9007199254740993") || !strings.Contains(projected[4].Text, "outcome unknown") || !strings.Contains(projected[5].Text, "cannot be recovered") {
		t.Fatal("JSON, state or loss missing")
	}
	plain := ToolResultMessage{Content: content}
	rich := plain
	rich.Details = d
	if EstimateMessageTokens(rich) <= EstimateMessageTokens(plain) {
		t.Fatal("structured/status tokens unaccounted")
	}
	if len(content) != 3 || d.Binding.ServiceID != "fixture" {
		t.Fatal("projection changed source")
	}
}

func TestStructuredResultJSONPreservesSourceSpelling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source string }{
		{"compact", `{"n":9007199254740993}`},
		{"escaping expansion", `{"text":"` + strings.Repeat("<", MaxStructuredResultBytes/5) + `"}`},
		{"formatting", " \n{\n  \"n\": 9007199254740993, \"exponent\": 1.20e+03\n}\t"},
		{"string spelling", `{"label":"<tag>&\u0061","line":"` + "\u2028" + `"}`},
		{"duplicate keys", `{ "n": 1, "n": 9007199254740993 }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := ToolResultMessage{Role: RoleToolResult, ToolCallID: "source", ToolName: "read", Timestamp: 1, Content: []ContentPart{NewTextContent("result").Part()}, Details: &ToolResultDetails{State: ExecutionReturned, StructuredContent: json.RawMessage(tc.source)}}
			encoded, err := MarshalAgentMessages([]AgentMessage{original})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := UnmarshalAgentMessages(encoded)
			if err != nil {
				t.Fatal(err)
			}
			got := decoded[0].(ToolResultMessage)
			if string(got.Details.StructuredContent) != tc.source {
				t.Fatalf("source JSON changed: got %q, want %q", got.Details.StructuredContent, tc.source)
			}
			if !reflect.DeepEqual(BoundToolResultView(got, 256), BoundToolResultView(original, 256)) {
				t.Fatal("persisting source changed the model view")
			}
		})
	}
}

func TestStructuredResultSourceCompanionValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source string }{
		{"different value", `{"state":"returned","structured_content":{"n":1},"structured_content_raw":"{ \"n\": 2 }"}`},
		{"number spelling", `{"state":"returned","structured_content":1,"structured_content_raw":"1.0"}`},
		{"missing value", `{"state":"returned","structured_content_raw":"{}"}`},
		{"malformed source", `{"state":"returned","structured_content":{},"structured_content_raw":"{"}`},
		{"empty source", `{"state":"returned","structured_content":{},"structured_content_raw":""}`},
		{"null companion", `{"state":"returned","structured_content":{},"structured_content_raw":null}`},
		{"object companion", `{"state":"returned","structured_content":{},"structured_content_raw":{}}`},
		{"oversize companion", `{"state":"returned","structured_content":{},"structured_content_raw":"` + strings.Repeat(" ", MaxStructuredResultBytes) + `{}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := resultDetails()
			target := original.Clone()
			if err := json.Unmarshal([]byte(tc.source), target); err == nil {
				t.Fatal("invalid source companion accepted")
			}
			if !reflect.DeepEqual(original, target) {
				t.Fatal("failed decode changed receiver")
			}
		})
	}
}

func TestStructuredResultSourceCompanionCompatibility(t *testing.T) {
	t.Parallel()
	for _, source := range []string{`{"n":9007199254740993}`, " { \"n\": 9007199254740993, \"label\": \"<html>\" } "} {
		t.Run(source, func(t *testing.T) {
			details := &ToolResultDetails{State: ExecutionReturned, StructuredContent: json.RawMessage(source)}
			encoded, err := json.Marshal(details)
			if err != nil {
				t.Fatal(err)
			}
			// An older details reader can still consume the existing JSON value.
			var legacy struct {
				State      ExecutionState  `json:"state"`
				Structured json.RawMessage `json:"structured_content"`
			}
			if err := json.Unmarshal(encoded, &legacy); err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(json.RawMessage(source))
			if err != nil {
				t.Fatal(err)
			}
			if legacy.State != ExecutionReturned || string(legacy.Structured) != string(canonical) {
				t.Fatal("legacy value changed")
			}
			if strings.Contains(string(encoded), `"structured_content_raw"`) == (source == string(canonical)) {
				t.Fatal("companion must appear only for noncanonical source")
			}
			var restored ToolResultDetails
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			second, err := json.Marshal(restored)
			if err != nil || string(second) != string(encoded) {
				t.Fatal("second encoding changed source record")
			}
			// Existing records without the companion remain supported.
			old, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(old, &restored); err != nil || string(restored.StructuredContent) != string(canonical) {
				t.Fatal("legacy record rejected or changed", err)
			}
		})
	}
}
