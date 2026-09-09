package openaicompletions

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
		Original: &llm.ImageOriginal{Data: []byte("original-secret-bytes"), MIMEType: "image/gif", Width: 4, Height: 4}}
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
	if !strings.Contains(text, "converted from image/gif to image/png") || !strings.Contains(text, "first frame only") {
		t.Fatal("conversion and animation policy missing from wire")
	}
	if strings.Contains(text, base64.StdEncoding.EncodeToString(img.Original.Data)) || strings.Contains(text, "original-secret-bytes") {
		t.Fatal("original bytes leaked into request")
	}

	var decoded []struct {
		Role       string `json:"role"`
		ToolCallID string `json:"tool_call_id"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 4 || decoded[1].Role != "tool" || decoded[1].ToolCallID != "one" || decoded[2].Role != "tool" || decoded[2].ToolCallID != "two" || decoded[3].Role != "user" {
		t.Fatalf("tool group interrupted: %s", wire)
	}
}
