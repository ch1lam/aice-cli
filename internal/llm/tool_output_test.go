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
