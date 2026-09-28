package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestManagedDesktopDisplayAndPrivateProgress(t *testing.T) {
	t.Parallel()
	var projection desktopDisplayProjection
	for _, operation := range managedCUAToolNames() {
		call := llm.ToolCall{ID: "managed", Name: mcpModelName(managedCUAKey, operation), Arguments: json.RawMessage(`{"text":"PRIVATE INPUT","query":"PRIVATE QUERY","pid":1,"window_id":2}`)}
		if display := projection.start(call); display == nil || display.Phase == "" || display.App != "" {
			t.Fatal("missing managed phase or invented application label", operation)
		}
		var output, diagnostics bytes.Buffer
		printer := newStreamPrinter(&output, &diagnostics)
		if err := printer.Accept(t.Context(), agent.AgentEvent{Type: agent.EventTypeToolExecutionStart, ToolCall: &call}); err != nil {
			t.Fatal(err)
		}
		if output.Len() != 0 || strings.Contains(diagnostics.String(), "PRIVATE") || strings.Contains(diagnostics.String(), "detail=") {
			t.Fatal("managed input leaked into progress")
		}
		for state, want := range map[llm.ExecutionState]string{llm.ExecutionNotDispatched: "Not dispatched", llm.ExecutionUnknown: "Outcome unknown", llm.ExecutionReturned: "Returned"} {
			result := llm.ToolResultMessage{Details: &llm.ToolResultDetails{State: state, Binding: &llm.ToolBinding{Source: "managed:computer-use", ServiceID: "cua", ToolName: operation}}}
			event := agent.AgentEvent{ToolCall: &call, ToolResult: &result}
			if got := projection.end(event); got == nil || got.Phase != want {
				t.Fatal("managed display lost execution state", operation, state, got)
			}
			result.Details.Binding.Source = "user:other"
			if got := projection.end(event); got.Phase != "Needs attention" {
				t.Fatal("foreign provenance presented as a managed result")
			}
		}
		call.Name = mcpModelName("user:other", operation)
		if projection.start(call) != nil {
			t.Fatal("ordinary service acquired managed presentation")
		}
	}
}
