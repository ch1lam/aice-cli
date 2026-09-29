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
	if len(view.Content) != 5 || view.Content[0].Text != "before" || view.Content[1].Type != ContentTypeImage || view.Content[2].Text != "after" || len(view.Details.StructuredContent) != 0 || !strings.HasPrefix(view.Content[3].Text, structuredPreviewLabel) {
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

func TestBoundResultViewLargeMixedResultHasStructuredPreview(t *testing.T) {
	// Match the shape that previously consumed the entire view with an image
	// and display text, hiding all structured-only references.
	raw := `{"n":9007199254740993,"ratio":1.2300e+04,"items":[` +
		strings.Repeat(`{"reference":"ref-123","label":"中文🙂"},`, 500) + `{}]}`
	source := ToolResultMessage{Role: RoleToolResult, ToolCallID: "provider/id\"\n中文", Content: []ContentPart{
		{Type: ContentTypeImage, Image: &ImageContent{MIMEType: "image/png", Data: []byte("image")}},
		NewTextContent(strings.Repeat("display-only text ", 1000)).Part(),
	}, Details: &ToolResultDetails{State: ExecutionReturned, StructuredContent: json.RawMessage(raw)}}
	before, _ := CloneAgentMessage(source)
	view := BoundToolResultView(source, 4096)
	if EstimateMessageTokens(view) > 4096 || len(view.Content) != 4 || view.Content[0].Type != ContentTypeImage || view.Content[1].Text == "" {
		t.Fatalf("mixed content missing or over budget: %d", EstimateMessageTokens(view))
	}
	prefix := strings.TrimPrefix(view.Content[2].Text, structuredPreviewLabel)
	if prefix == view.Content[2].Text || !utf8.ValidString(prefix) || !strings.HasPrefix(raw, prefix) || !strings.Contains(prefix, `"reference":"ref-123"`) || !strings.Contains(prefix, `1.2300e+04`) {
		t.Fatal("structured preview missing or source lexemes changed")
	}
	var selector struct {
		CallID  string `json:"call_id"`
		Section string `json:"section"`
	}
	if err := json.Unmarshal([]byte(strings.Split(view.Content[3].Text, "\n")[1]), &selector); err != nil || selector.CallID != source.ToolCallID || selector.Section != "structured" {
		t.Fatal("readback selector is not usable", err)
	}
	if len(view.Details.StructuredContent) != 0 || !reflect.DeepEqual(before, source) || !json.Valid(source.Details.StructuredContent) {
		t.Fatal("partial JSON exposed as structured data or durable source changed")
	}
}

func TestBoundResultViewSmallBudgetAndLongSelector(t *testing.T) {
	for _, id := range []string{"normal", strings.Repeat("x", 4096), strings.Repeat("中文", 4096)} {
		source := ToolResultMessage{Role: RoleToolResult, ToolCallID: id, IsError: true,
			Content: []ContentPart{NewTextContent(strings.Repeat("中文", 2000)).Part()},
			Details: &ToolResultDetails{State: ExecutionUnknown, Loss: strings.Repeat("loss ", 1000), StructuredContent: json.RawMessage(`{"n":9007199254740993}`)}}
		view := BoundToolResultView(source, 256)
		if EstimateMessageTokens(view) > 256 {
			t.Fatalf("long selector exceeded minimum budget: %d", EstimateMessageTokens(view))
		}
		notice := view.Content[len(view.Content)-1].Text
		if id != "normal" && (!strings.Contains(notice, "tool-call envelope") || strings.Contains(notice, `{"call_id"`)) {
			t.Fatal("long selector was truncated or invented")
		}
	}
}
