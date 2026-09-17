package llm

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestCloneAgentMessagesPreservesMetadataAndOwnership(t *testing.T) {
	image := &ImageContent{Data: []byte("preview"), MIMEType: "image/png", Original: &ImageOriginal{Data: []byte("original"), MIMEType: "image/png", Width: 10, Height: 10}, Region: &ImageRegion{X: 2, Width: 3, Height: 3}, Source: "fixture", ID: "image"}
	user := UserMessage{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeImage, Image: image}}, Timestamp: 1}
	assistant := NewAssistantMessage(Model{API: "test", Provider: "test", ID: "test"})
	assistant.Content = []ContentPart{NewThinkingContent("reasoning", "opaque-signature").Part(), {Type: ContentTypeToolCall, ToolCall: &ToolCall{ID: "call", Name: "read", Arguments: json.RawMessage(`{"path":"file"}`), Signature: "tool-signature"}}}
	assistant.Usage.Cost = &Cost{Input: 1, Output: 2, Total: 3}
	assistant.StopReason = StopReasonToolUse
	result := ToolResultMessage{Role: RoleToolResult, ToolCallID: "call", ToolName: "read", Content: []ContentPart{NewTextContent("result").Part(), {Type: ContentTypeImage, Image: image}}, Diff: ToolDiff{Text: "diff", StatsKnown: true}, Truncation: ToolTruncation{Reason: TruncationByteLimit, NextOffset: 3}, Timestamp: 3}
	summary, _ := NewCompactionSummaryMessage("summary", 100)
	messages := []AgentMessage{user, assistant, result, summary}
	clone, err := CloneAgentMessages(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(messages, clone) {
		t.Fatal("clone lost message metadata")
	}
	copiedImage := clone[0].(UserMessage).Content[0].Image
	copiedImage.Data[0] = 'X'
	copiedImage.Original.Data[0] = 'X'
	copiedImage.Region.X = 999
	copiedAssistant := clone[1].(AssistantMessage)
	copiedAssistant.Content[0].Text = "changed"
	copiedAssistant.Content[1].ToolCall.Arguments[0] = '!'
	copiedAssistant.Usage.Cost.Total = 999
	clone[2].(ToolResultMessage).Content[0].Text = "changed"
	if string(image.Data) != "preview" || string(image.Original.Data) != "original" || image.Region.X != 2 || assistant.Content[0].Text != "reasoning" || assistant.Usage.Cost.Total != 3 || !json.Valid(assistant.Content[1].ToolCall.Arguments) || result.Content[0].Text != "result" {
		t.Fatal("clone aliases caller payload")
	}
	// Source mutation must not affect another separately copied occurrence.
	image.Data[0] = 'Y'
	if string(clone[2].(ToolResultMessage).Content[1].Image.Data) != "preview" {
		t.Fatal("repeated image shares mutable bytes")
	}
}

func TestCloneAgentMessagesRejectsInvalidValues(t *testing.T) {
	assistant := NewAssistantMessage(Model{API: "test", Provider: "test", ID: "test"})
	assistant.Usage.Cost = &Cost{Total: math.NaN()}
	for _, message := range []AgentMessage{nil, UserMessage{Role: RoleUser}, assistant, UserMessage{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeImage}}}} {
		if _, err := CloneAgentMessage(message); err == nil {
			t.Fatalf("accepted invalid %T", message)
		}
	}
	for _, messages := range [][]AgentMessage{nil, {}} {
		got, err := CloneAgentMessages(messages)
		if err != nil || !reflect.DeepEqual(got, messages) {
			t.Fatal("nil/empty slice changed")
		}
	}
}
