package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundResultViewPreservesSourceAndState(t *testing.T) {
	for _, text := range []string{strings.Repeat("abc ", 20000), strings.Repeat("中文🙂", 10000)} {
		source := ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", Content: []ContentPart{NewTextContent(text).Part()}, IsError: true, Details: &ToolResultDetails{State: ExecutionUnknown, Loss: strings.Repeat("source loss ", 300), StructuredContent: json.RawMessage(`{"n":9007199254740993}`)}}
		before, _ := CloneAgentMessage(source)
		view := BoundToolResultView(source, 512)
		if EstimateMessageTokens(view) > 512 || view.Details.State != ExecutionUnknown || !view.IsError {
			t.Fatalf("invalid bounded view: %d", EstimateMessageTokens(view))
		}
		if !strings.Contains(view.Content[len(view.Content)-1].Text, "tool_result_read") || !utf8.ValidString(view.Content[0].Text) {
			t.Fatal("missing readback or split UTF-8")
		}
		if !reflect.DeepEqual(before, source) {
			t.Fatal("projection changed source")
		}
		view.Details.Loss = "changed"
		if source.Details.Loss == "changed" {
			t.Fatal("metadata aliases source")
		}
	}
}

func TestBoundResultViewOrderJSONAndImages(t *testing.T) {
	source := ToolResultMessage{Role: RoleToolResult, ToolCallID: "c", Content: []ContentPart{NewTextContent("before").Part(), {Type: ContentTypeImage, Image: &ImageContent{MIMEType: "image/png", Data: []byte("image")}}, NewTextContent("after").Part()}, Details: &ToolResultDetails{State: ExecutionReturned, StructuredContent: json.RawMessage(`{"payload":"` + strings.Repeat("x", 30000) + `","n":9007199254740993}`)}}
	view := BoundToolResultView(source, 2000)
	if len(view.Content) != 4 || view.Content[0].Text != "before" || view.Content[1].Type != ContentTypeImage || view.Content[2].Text != "after" || len(view.Details.StructuredContent) != 0 {
		t.Fatal("order or whole JSON invariant failed")
	}
	view.Content[1].Image.Data[0] = '!'
	if string(source.Content[1].Image.Data) != "image" || !json.Valid(source.Details.StructuredContent) {
		t.Fatal("source payload changed")
	}
	source.Content = []ContentPart{{Type: ContentTypeImage, Image: &ImageContent{MIMEType: "image/png", Data: make([]byte, 5<<20)}}}
	source.Details.StructuredContent = nil
	view = BoundToolResultView(source, 4096)
	if len(view.Content) != 1 || view.Content[0].Type != ContentTypeText {
		t.Fatal("large image bypassed view bound")
	}
}
