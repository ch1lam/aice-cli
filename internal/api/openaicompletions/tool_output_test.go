package openaicompletions

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestOrderedStructuredToolResultProjection(t *testing.T) {
	t.Parallel()
	model := llm.Model{ID: "vision", Provider: "fixture", API: API}
	result := llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-one", ToolName: "inspect", IsError: true,
		Content: []llm.ContentPart{
			llm.NewTextContent("before-image-one").Part(),
			{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image-one-bytes"), MIMEType: "image/png"}},
			llm.NewTextContent("between-images").Part(),
			{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image-two-bytes"), MIMEType: "image/png"}},
			llm.NewTextContent("after-image-two").Part(),
		}, Details: &llm.ToolResultDetails{State: llm.ExecutionUnknown, StructuredContent: json.RawMessage(`{"count":9007199254740993}`), Loss: "one block exceeded limit", Binding: &llm.ToolBinding{Source: "fixture-source", ServiceID: "fixture-id", ConnectionFingerprint: "private-connection-fingerprint", ToolName: "raw-tool", SchemaFingerprint: "private-schema-fingerprint"}}}
	messages := []llm.Message{
		llm.AssistantMessage{Role: llm.RoleAssistant, API: API, Provider: model.Provider, ModelID: model.ID, StopReason: llm.StopReasonToolUse, Content: []llm.ContentPart{
			{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "call-one", Name: "inspect", Arguments: json.RawMessage(`{}`)}},
		}}, result,
	}
	converted, err := messageParams(messages, model)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	previous := -1
	for _, needle := range []string{"before-image-one", base64.StdEncoding.EncodeToString([]byte("image-one-bytes")), "between-images", base64.StdEncoding.EncodeToString([]byte("image-two-bytes")), "after-image-two", "9007199254740993", "outcome unknown", "cannot be recovered"} {
		position := strings.Index(text, needle)
		if position <= previous {
			t.Fatalf("missing or reordered %q: %s", needle, text)
		}
		previous = position
	}
	if strings.Contains(text, "private-connection-fingerprint") || strings.Contains(text, "private-schema-fingerprint") {
		t.Fatal("binding leaked into provider input")
	}
	if len(result.Content) != 5 || result.Content[2].Text != "between-images" {
		t.Fatal("source result mutated")
	}
}
