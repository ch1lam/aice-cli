package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEditDiffProjectionAfterReplayAndPrintContract(t *testing.T) {
	for _, tt := range []struct {
		name            string
		legacy, isError bool
		err             error
	}{
		{name: "success"}, {name: "legacy", legacy: true}, {name: "result failure", isError: true}, {name: "execution failure", err: errors.New("failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := llm.NewToolResultMessage(llm.ToolResult{CallID: "edit-1", Name: "edit", Content: []llm.ContentPart{llm.NewTextContent("summary").Part()}, IsError: tt.isError})
			if err != nil {
				t.Fatal(err)
			}
			if !tt.legacy {
				result.Diff = llm.ToolDiff{Text: "@@ -1 +1 @@\n-old\n+new\n", Truncated: true}
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
			event := agent.AgentEvent{Type: agent.EventTypeToolExecutionEnd, ToolCall: &llm.ToolCall{ID: "edit-1", Name: "edit"}, ToolResult: &replay, Err: tt.err}
			display := translateAgentEvent(event)
			if display.Tool.Failed != (tt.isError || tt.err != nil) {
				t.Fatal(display)
			}
			if tt.legacy || tt.isError || tt.err != nil {
				if display.Tool.Diff.Text != "" || display.Tool.Diff.Truncated {
					t.Fatal(display)
				}
			} else if display.Tool.Diff.Text != result.Diff.Text || !display.Tool.Diff.Truncated {
				t.Fatal(display)
			}
			for _, format := range []string{"text", "json"} {
				var output, diagnostics bytes.Buffer
				printer, err := newPrintSink(format, &output, &diagnostics)
				if err != nil {
					t.Fatal(err)
				}
				if err := printer.Accept(t.Context(), event); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(output.String()+diagnostics.String(), "@@") || strings.Contains(output.String(), `"diff"`) {
					t.Fatal("diff changed print contract")
				}
			}
		})
	}
}
