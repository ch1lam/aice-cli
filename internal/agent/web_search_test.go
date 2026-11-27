package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func doneScript(message llm.AssistantMessage) *streamScript {
	return &streamScript{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message},
	}}
}

func TestLoopResumesPausedServerToolTurn(t *testing.T) {
	t.Parallel()
	modelInfo := testModel()
	search := llm.NewWebSearchPart(llm.WebSearchCall{ID: "srvtoolu_1", Action: llm.WebSearchActionSearch, Queries: []string{"go release"},
		Replay: []json.RawMessage{json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_1"}`)}})
	paused := assistantMessage(modelInfo, llm.StopReasonPause, search)
	answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("Go 1.27.4"))
	model := &scriptedModel{scripts: []*streamScript{doneScript(paused), doneScript(answer)}}
	result, err := mustLoop(t, model, nil).Run(t.Context(), testInput(modelInfo, mustPrompt(t, "latest go?")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 || len(result.ModelRounds) != 2 {
		t.Fatalf("requests = %d rounds = %d", len(model.requests), len(result.ModelRounds))
	}
	// The paused response goes back unchanged as the last message, without a
	// new user message, so the provider resumes it.
	resumed := model.requests[1].Messages
	last, ok := resumed[len(resumed)-1].(llm.AssistantMessage)
	if len(resumed) != 2 || !ok || last.StopReason != llm.StopReasonPause || last.Content[0].WebSearch.ID != "srvtoolu_1" {
		t.Fatalf("resumed request = %#v", resumed)
	}
}

func TestLoopHostedWebSearchReplacesLocalToolOnlyForCapableModels(t *testing.T) {
	t.Parallel()
	policy := &llm.WebSearch{AllowedDomains: []string{"go.dev"}, Replaces: "web_search"}
	for _, tc := range []struct {
		name     string
		support  *llm.WebSearchSupport
		thinking llm.ThinkingLevel
		hosted   bool
	}{
		{"capable", &llm.WebSearchSupport{Tool: llm.WebSearchToolStandard, AllowedDomains: true}, llm.ThinkingLevelOff, true},
		{"no support", nil, llm.ThinkingLevelOff, false},
		{"cannot enforce domains", &llm.WebSearchSupport{Tool: llm.WebSearchToolStandard}, llm.ThinkingLevelOff, false},
		{"needs thinking, off", &llm.WebSearchSupport{Tool: llm.WebSearchToolDeepSeek, RequiresThinking: true, AllowedDomains: true}, llm.ThinkingLevelOff, false},
		{"needs thinking, on", &llm.WebSearchSupport{Tool: llm.WebSearchToolDeepSeek, RequiresThinking: true, AllowedDomains: true}, llm.ThinkingLevelHigh, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			modelInfo := testModel()
			modelInfo.WebSearch = tc.support
			answer := assistantMessage(modelInfo, llm.StopReasonStop, textPart("ok"))
			model := &scriptedModel{scripts: []*streamScript{doneScript(answer)}}
			input := testInput(modelInfo, mustPrompt(t, "search"))
			input.Options.Thinking = tc.thinking
			input.WebSearch = policy
			loop := mustLoop(t, model, []agent.Tool{newFakeTool("web_search", nil), newFakeTool("read", nil)})
			if _, err := loop.Run(t.Context(), input, nil); err != nil {
				t.Fatal(err)
			}
			request := model.requests[0]
			var names []string
			for _, definition := range request.Tools {
				names = append(names, definition.Name)
			}
			if tc.hosted {
				if request.WebSearch == nil || request.WebSearch.AllowedDomains[0] != "go.dev" || len(names) != 1 || names[0] != "read" {
					t.Fatalf("hosted request: web_search=%+v tools=%v", request.WebSearch, names)
				}
				return
			}
			if request.WebSearch != nil || len(names) != 2 {
				t.Fatalf("local request: web_search=%+v tools=%v", request.WebSearch, names)
			}
		})
	}
}
