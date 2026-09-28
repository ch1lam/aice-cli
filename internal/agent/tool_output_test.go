package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestLoopPreservesExplicitPartialOutcome(t *testing.T) {
	t.Parallel()
	for _, state := range []llm.ExecutionState{llm.ExecutionReturned, llm.ExecutionUnknown} {
		t.Run(string(state), func(t *testing.T) {
			modelInfo := testModel()
			first := assistantMessage(modelInfo, llm.StopReasonToolUse, toolCallPart("call", "remote", `{}`))
			last := assistantMessage(modelInfo, llm.StopReasonStop, textPart("inspect before retry"))
			model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(first)}, {events: terminalEvents(last)}}}
			tool := newFakeTool("remote", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
				return llm.ToolResult{Content: []llm.ContentPart{textPart("partial before"), {Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, textPart("partial after")}, Details: &llm.ToolResultDetails{State: state, StructuredContent: json.RawMessage(`{"committed":true}`)}}, errors.New("connection ended")
			})
			input := testInput(modelInfo, mustPrompt(t, "inspect"))
			var recorded []llm.ToolResultMessage
			input.MessageRecorder = func(_ context.Context, m llm.AgentMessage) error {
				if r, ok := m.(llm.ToolResultMessage); ok {
					recorded = append(recorded, r)
				}
				return nil
			}
			result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(tool.calls) != 1 || len(recorded) != 1 {
				t.Fatalf("calls=%d recorded=%d", len(tool.calls), len(recorded))
			}
			got := recorded[0]
			if got.Details.State != state || !got.IsError || len(got.Content) != 4 || got.Content[1].Type != llm.ContentTypeImage || got.Content[2].Text != "partial after" {
				t.Fatalf("partial lost: %#v", got)
			}
			if !reflect.DeepEqual(got, result.ModelRounds[0].ToolResults[0]) || !reflect.DeepEqual(got, model.requests[1].Messages[len(model.requests[1].Messages)-1]) {
				t.Fatal("recorded/model outcomes differ")
			}
		})
	}
}

func TestLoopInvalidExplicitOutcomeRemainsUnknown(t *testing.T) {
	t.Parallel()
	modelInfo := testModel()
	first := assistantMessage(modelInfo, llm.StopReasonToolUse, toolCallPart("call", "remote", `{}`))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(first)}, {events: terminalEvents(assistantMessage(modelInfo, llm.StopReasonStop, textPart("stop")))}}}
	tool := newFakeTool("remote", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{Details: &llm.ToolResultDetails{State: "invalid"}}, nil
	})
	result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), testInput(modelInfo, mustPrompt(t, "inspect")), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := result.ModelRounds[0].ToolResults[0]
	if got.Details == nil || got.Details.State != llm.ExecutionUnknown || got.Details.Loss == "" || !got.IsError || len(tool.calls) != 1 {
		t.Fatalf("invalid outcome lost uncertainty: %#v", got)
	}
}
