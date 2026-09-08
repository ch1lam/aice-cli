package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"github.com/ch1lam/aice-cli/internal/llm"
	"strings"
	"testing"
)

func TestToolImageWireContentAndPairing(t *testing.T) {
	t.Parallel()
	model := llm.Model{ID: "vision", Provider: "test", API: API}
	img := llm.ImageContent{Data: []byte("view"), MIMEType: "image/png", ID: "image:test", Width: 2, Height: 2,
		Original: &llm.ImageOriginal{Data: []byte("original-secret-bytes"), MIMEType: "image/png", Width: 4, Height: 4}}
	calls := []llm.ContentPart{
		{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "one", Name: "read", Arguments: json.RawMessage(`{"path":"one.png"}`)}},
		{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "two", Name: "read", Arguments: json.RawMessage(`{"path":"two.txt"}`)}},
	}
	messages := []llm.Message{
		llm.AssistantMessage{Role: llm.RoleAssistant, Provider: model.Provider, API: API, ModelID: model.ID, Content: calls, StopReason: llm.StopReasonToolUse},
		llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "one", ToolName: "read", Content: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &img}}},
		llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "two", ToolName: "read", Content: []llm.ContentPart{llm.NewTextContent("second result").Part()}},
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
	if !strings.Contains(text, base64.StdEncoding.EncodeToString(img.Data)) || !strings.Contains(text, "image:test") {
		t.Fatal("image bytes or identity missing from wire")
	}
	if strings.Contains(text, base64.StdEncoding.EncodeToString(img.Original.Data)) || strings.Contains(text, "original-secret-bytes") {
		t.Fatal("original bytes leaked into request")
	}
}
