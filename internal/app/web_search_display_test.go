package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestHostedSearchDisplaysAsFinishedProviderTool(t *testing.T) {
	t.Parallel()
	part := llm.NewWebSearchPart(llm.WebSearchCall{ID: "ws_1", Action: llm.WebSearchActionSearch, Queries: []string{"latest go"},
		Sources: []llm.WebSearchSource{{URL: "https://go.dev", Title: "Go"}}, Replay: []json.RawMessage{json.RawMessage(`{}`)}})
	event := translateAgentEvent(agent.AgentEvent{Type: agent.EventTypeMessageUpdate,
		AssistantMessageEvent: &llm.Event{Type: llm.EventTypeWebSearch, Content: &part}})
	if event == nil || event.Kind != interaction.EventProviderTool {
		t.Fatalf("event = %+v", event)
	}
	tool := event.Tool
	if tool.ID != "ws_1" || tool.Name != "web_search" || tool.Detail != "latest go" || tool.Failed ||
		tool.Evidence == nil || tool.Evidence.Sources[0].URL != "https://go.dev" ||
		!tool.Output.Available || !strings.Contains(tool.Output.Text, `searched "latest go"`) {
		t.Fatalf("tool = %+v", tool)
	}

	failed := hostedSearchDisplay(llm.WebSearchCall{ID: "ws_2", Action: llm.WebSearchActionOpenPage, URL: "https://go.dev/dl/", Error: "unavailable"})
	if !failed.Failed || failed.Detail != "https://go.dev/dl/" || !strings.Contains(failed.Output.Text, "failed: unavailable") {
		t.Fatalf("failed = %+v", failed)
	}

	var builder strings.Builder
	appendCompactionContent(&builder, []llm.ContentPart{part}, 1000)
	if !strings.Contains(builder.String(), `[Provider web search: searched "latest go"]`) || !strings.Contains(builder.String(), "https://go.dev") {
		t.Fatalf("compaction = %q", builder.String())
	}
}
