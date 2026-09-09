package agent_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestLoopReadEOFErrorReachesModelAndRecorder(t *testing.T) {
	t.Parallel()
	for _, offset := range []int{3, 1000000} {
		t.Run(fmt.Sprintf("offset=%d", offset), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			workspace, err := tool.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			read, err := tool.NewRead(workspace)
			if err != nil {
				t.Fatal(err)
			}
			modelInfo := testModel()
			first := assistantMessage(modelInfo, llm.StopReasonToolUse,
				toolCallPart("bad-read", "read", fmt.Sprintf(`{"path":"notes.txt","offset":%d}`, offset)))
			second := assistantMessage(modelInfo, llm.StopReasonToolUse,
				toolCallPart("retry-read", "read", `{"path":"notes.txt","offset":2,"limit":1}`))
			last := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
			model := &scriptedModel{scripts: []*streamScript{
				{events: terminalEvents(first)},
				{events: terminalEvents(second)},
				{events: terminalEvents(last)},
			}}
			loop := mustLoop(t, model, []agent.Tool{read})
			input := testInput(modelInfo, mustPrompt(t, "read notes"))
			var recorded []llm.ToolResultMessage
			input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
				if result, ok := message.(llm.ToolResultMessage); ok {
					recorded = append(recorded, result)
				}
				return nil
			}
			var events []agent.AgentEvent
			result, err := loop.Run(t.Context(), input, collectEvents(&events))
			if err != nil {
				t.Fatalf("Run() error = %v; tool parameter errors should allow correction", err)
			}
			if len(model.requests) != 3 || len(result.ModelRounds) != 3 || len(recorded) != 2 {
				t.Fatalf("requests=%d rounds=%d recorded results=%d", len(model.requests), len(result.ModelRounds), len(recorded))
			}
			failed := recorded[0]
			want := fmt.Sprintf("offset %d is beyond end of file (2 lines total)", offset)
			if failed.ToolCallID != "bad-read" || failed.ToolName != "read" || !failed.IsError ||
				len(failed.Content) != 1 || !strings.Contains(failed.Content[0].Text, want) ||
				!strings.Contains(failed.Content[0].Text, "notes.txt") {
				t.Fatalf("error result = %#v, want paired read error containing %q", failed, want)
			}
			request := model.requests[1]
			if !reflect.DeepEqual(request.Messages[len(request.Messages)-1], failed) ||
				!reflect.DeepEqual(result.ModelRounds[0].ToolResults, []llm.ToolResultMessage{failed}) {
				t.Fatal("model context or retained round differs from recorded error result")
			}
			if recovered := recorded[1]; recovered.IsError || recovered.ToolCallID != "retry-read" ||
				len(recovered.Content) != 1 || recovered.Content[0].Text != "two\n" {
				t.Fatalf("corrected read result = %#v", recovered)
			}
			ends := 0
			for _, event := range events {
				if event.Type == agent.EventTypeToolExecutionEnd && event.ToolCall.ID == "bad-read" {
					ends++
					if event.ToolResult == nil || !reflect.DeepEqual(*event.ToolResult, failed) {
						t.Fatalf("tool end result = %#v", event.ToolResult)
					}
				}
			}
			if ends != 1 {
				t.Fatalf("failed tool end events = %d, want 1", ends)
			}
		})
	}
}
