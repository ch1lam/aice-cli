package app

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestDisplayToolOutputRetainsRecordedContent(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"read", "bash", "skill"} {
		t.Run(name, func(t *testing.T) {
			original := strings.Repeat("中文", 12000)
			result, err := llm.NewToolResultMessage(llm.ToolResult{CallID: "call", Name: name,
				Content: []llm.ContentPart{llm.NewTextContent(original).Part()}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := llm.MarshalAgentMessages([]llm.AgentMessage{result})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := llm.UnmarshalAgentMessages(raw)
			if err != nil {
				t.Fatal(err)
			}
			replay := restored[0].(llm.ToolResultMessage)
			display := translateAgentEvent(agent.AgentEvent{Type: agent.EventTypeToolExecutionEnd,
				ToolCall: &llm.ToolCall{ID: "call", Name: name}, ToolResult: &replay})
			output := display.Tool.Output
			if !output.Available || !output.Truncated || !utf8.ValidString(output.Text) ||
				len(output.Text) > maximumDisplayToolOutputBytes || !strings.HasPrefix(original, output.Text) {
				t.Fatal("invalid bounded output projection")
			}
			if result.Content[0].Text != original || replay.Content[0].Text != original {
				t.Fatal("display projection changed recorded content")
			}
		})
	}
}

func TestDisplayToolOutputEmptyFailureAndImage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		result    *llm.ToolResultMessage
		err       error
		want      string
		available bool
	}{
		{name: "absent"},
		{name: "empty", result: &llm.ToolResultMessage{}, available: true},
		{name: "failure", err: errors.New("permission denied"), want: "permission denied", available: true},
		{name: "image", result: &llm.ToolResultMessage{Content: []llm.ContentPart{{Type: llm.ContentTypeImage}}}, want: "[Image result]", available: true},
		{name: "multiple blocks", result: &llm.ToolResultMessage{Content: []llm.ContentPart{llm.NewTextContent("one").Part(), llm.NewTextContent("two").Part()}}, want: "one\ntwo", available: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := displayToolOutput(agent.AgentEvent{ToolResult: tt.result, Err: tt.err})
			if got.Text != tt.want || got.Available != tt.available || got.Truncated {
				t.Fatalf("output = %#v", got)
			}
		})
	}
}
