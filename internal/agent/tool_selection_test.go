package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type selectionTool struct {
	proposal []agent.ToolReference
	failed   bool
}

func (s selectionTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: "tool_search", Description: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (s selectionTool) Execute(context.Context, llm.ToolCall) (llm.ToolResult, error) {
	return llm.ToolResult{}, errors.New("selection capability was not used")
}
func (s selectionTool) SelectTools(_ context.Context, call llm.ToolCall) (llm.ToolResult, []agent.ToolReference, error) {
	return llm.ToolResult{CallID: call.ID, Content: []llm.ContentPart{textPart("proposed next-round tools")}, IsError: s.failed}, slices.Clone(s.proposal), nil
}

type selectionCatalog struct {
	mu       sync.Mutex
	entries  []agent.CatalogTool
	resolves int
}

func (c *selectionCatalog) Resolve(_ context.Context, ids []string) ([]agent.CatalogTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolves++
	var out []agent.CatalogTool
	for _, entry := range c.entries {
		if slices.Contains(ids, entry.Reference.ID) {
			out = append(out, entry)
		}
	}
	return out, nil
}
func (c *selectionCatalog) Check(_ context.Context, ref agent.ToolReference) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.Reference == ref {
			return nil
		}
	}
	return errors.New("version revoked")
}
func (c *selectionCatalog) replace(entries ...agent.CatalogTool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = slices.Clone(entries)
}
func catalogEntry(id, revision string, tool agent.Tool) agent.CatalogTool {
	return agent.CatalogTool{Reference: agent.ToolReference{ID: id, Revision: revision}, Tool: tool}
}
func hasDefinition(request llm.Request, name string) bool {
	for _, d := range request.Tools {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestToolSelectionTakesEffectOnlyAfterCompleteRecordedRound(t *testing.T) {
	t.Parallel()
	info := testModel()
	remote := newFakeTool("mcp_fixture_read", nil)
	entry := catalogEntry("user/fixture/read", "v1", remote)
	catalog := &selectionCatalog{entries: []agent.CatalogTool{entry}}
	first := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("search", "tool_search", `{}`), toolCallPart("too-soon", remote.definition.Name, `{}`))
	second := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("loaded", remote.definition.Name, `{}`))
	last := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(first)}, {events: terminalEvents(second)}, {events: terminalEvents(last)}}}
	loop := mustLoop(t, model, []agent.Tool{selectionTool{proposal: []agent.ToolReference{entry.Reference}}})
	input := testInput(info, mustPrompt(t, "find and read"))
	input.Catalog = catalog
	var recorded []llm.AgentMessage
	input.MessageRecorder = captureMessages(&recorded)
	result, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasDefinition(model.requests[0], remote.definition.Name) || !hasDefinition(model.requests[1], remote.definition.Name) {
		t.Fatal("schema appeared in wrong round")
	}
	if len(remote.calls) != 1 || remote.calls[0].ID != "loaded" || !result.ModelRounds[0].ToolResults[1].IsError {
		t.Fatal("unoffered tool executed")
	}
	assertRecordedMessages(t, recorded, result.Messages())
	// Resumed history does not restore selection. Reusing Loop cannot retain it.
	model.scripts = append(model.scripts, &streamScript{events: terminalEvents(second)}, &streamScript{events: terminalEvents(last)})
	input.History = result.Messages()
	resumed, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasDefinition(model.requests[3], remote.definition.Name) || !resumed.ModelRounds[0].ToolResults[0].IsError || len(remote.calls) != 1 {
		t.Fatal("selection survived Run or history replay")
	}
}

func TestToolSelectionRecordingFailureDoesNotPublish(t *testing.T) {
	t.Parallel()
	info := testModel()
	remote := newFakeTool("remote", nil)
	entry := catalogEntry("id", "v1", remote)
	catalog := &selectionCatalog{entries: []agent.CatalogTool{entry}}
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("s", "tool_search", `{}`)))}}}
	loop := mustLoop(t, model, []agent.Tool{selectionTool{proposal: []agent.ToolReference{entry.Reference}}})
	input := testInput(info, mustPrompt(t, "find"))
	input.Catalog = catalog
	diskErr := errors.New("disk failed")
	input.MessageRecorder = func(_ context.Context, m llm.AgentMessage) error {
		if m.MessageRole() == llm.RoleToolResult {
			return diskErr
		}
		return nil
	}
	_, err := loop.Run(t.Context(), input, nil)
	if !errors.Is(err, diskErr) || len(model.requests) != 1 || len(remote.calls) != 0 || catalog.resolves != 1 {
		t.Fatalf("selection published after recording failure: %v", err)
	}
}

func TestSelectedVersionCheckedAfterStreamAndApproval(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"stream", "approval"} {
		t.Run(phase, func(t *testing.T) {
			info := testModel()
			old := newFakeTool("remote", nil)
			newer := newFakeTool("remote", nil)
			newer.definition.InputSchema = json.RawMessage(`{"type":"object","required":["updated"]}`)
			catalog := &selectionCatalog{entries: []agent.CatalogTool{catalogEntry("id", "v1", old)}}
			replace := func() { catalog.replace(catalogEntry("id", "v2", newer)) }
			first := &streamScript{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("old", "remote", `{}`)))}
			var options []agent.LoopOption
			if phase == "stream" {
				first.onNext = func(i int) {
					if i == 1 {
						replace()
					}
				}
			} else {
				options = append(options, agent.WithGuard(fixedDecisionGuard{result: agent.GuardResult{Decision: agent.GuardAsk, Approvals: []agent.GuardApproval{{RuleID: "tool", Reason: "tool permission"}}}}), agent.WithGuardAskHandler(func(context.Context, llm.ToolCall, agent.GuardApproval) (agent.GuardAskReply, error) {
					replace()
					return agent.GuardAskReply{Decision: agent.GuardAllow}, nil
				}))
			}
			model := &scriptedModel{scripts: []*streamScript{first, {events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("new schema visible")))}}}
			loop := mustLoop(t, model, nil, append([]agent.LoopOption{agent.WithGuard(allowAllGuard{})}, options...)...)
			input := testInput(info, mustPrompt(t, "inspect"))
			input.Catalog = catalog
			input.PinnedTools = []string{"id"}
			result, err := loop.Run(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(old.calls) != 0 || len(newer.calls) != 0 || !result.ModelRounds[0].ToolResults[0].IsError {
				t.Fatal("stale version dispatched")
			}
			if strings.Contains(string(model.requests[0].Tools[0].InputSchema), "updated") || !strings.Contains(string(model.requests[1].Tools[0].InputSchema), "updated") {
				t.Fatal("request snapshot mutated or refresh missing")
			}
		})
	}
}

func TestToolSelectionRejectsInvalidProposalsAndOversizedSchemas(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"stale", "oversized", "collision", "failed search", "too many", "missing"} {
		t.Run(kind, func(t *testing.T) {
			info := testModel()
			remote := newFakeTool("remote", nil)
			entry := catalogEntry("id", "v1", remote)
			proposal := []agent.ToolReference{entry.Reference}
			selector := selectionTool{}
			switch kind {
			case "stale":
				proposal[0].Revision = "old"
			case "oversized":
				remote.definition.Description = strings.Repeat("x", 4*int(agent.ToolSchemaBudget(info)))
			case "collision":
				remote.definition.Name = "tool_search"
			case "failed search":
				selector.failed = true
			case "too many":
				proposal = slices.Repeat(proposal, 6)
			case "missing":
				entry.Reference.ID = "other"
			}
			selector.proposal = proposal
			model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("s", "tool_search", `{}`)))}, {events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))}}}
			input := testInput(info, mustPrompt(t, "find"))
			input.Catalog = &selectionCatalog{entries: []agent.CatalogTool{entry}}
			result, err := mustLoop(t, model, []agent.Tool{selector}).Run(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.ModelRounds[0].ToolResults[0].IsError || len(model.requests[1].Tools) != 1 {
				t.Fatal("invalid selection applied")
			}
		})
	}
}

func TestPinnedToolSchemaBudgetFailsBeforeModel(t *testing.T) {
	t.Parallel()
	info := testModel()
	remote := newFakeTool("remote", nil)
	remote.definition.Description = strings.Repeat("x", 4*int(agent.ToolSchemaBudget(info)))
	model := &scriptedModel{}
	input := testInput(info, mustPrompt(t, "read"))
	input.Catalog = &selectionCatalog{entries: []agent.CatalogTool{catalogEntry("id", "v1", remote)}}
	input.PinnedTools = []string{"id"}
	_, err := mustLoop(t, model, nil, agent.WithGuard(allowAllGuard{})).Run(t.Context(), input, nil)
	if !errors.Is(err, agent.ErrToolSelection) || len(model.requests) != 0 {
		t.Fatalf("oversized pinned schema requested: %v", err)
	}
	input.Catalog = &selectionCatalog{}
	if _, err := mustLoop(t, model, nil).Run(t.Context(), input, nil); err == nil {
		t.Fatal("catalog without guard admitted")
	}
}

func TestToolSchemaBudgets(t *testing.T) {
	for _, tt := range []struct{ window, want int64 }{{0, 4096}, {10000, 500}, {1000000, 8192}, {10, 1}} {
		t.Run(fmt.Sprint(tt.window), func(t *testing.T) {
			if got := agent.ToolSchemaBudget(llm.Model{ContextWindow: tt.window}); got != tt.want {
				t.Fatalf("budget=%d want=%d", got, tt.want)
			}
		})
	}
}

type selectionModelFunc func(context.Context, llm.Request) (llm.Stream, error)

func (f selectionModelFunc) Stream(ctx context.Context, r llm.Request) (llm.Stream, error) {
	return f(ctx, r)
}

func TestConcurrentRunsDoNotShareSelections(t *testing.T) {
	t.Parallel()
	info := testModel()
	remote := newFakeTool("remote", nil)
	entry := catalogEntry("id", "v1", remote)
	catalog := &selectionCatalog{entries: []agent.CatalogTool{entry}}
	selected := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := selectionModelFunc(func(ctx context.Context, r llm.Request) (llm.Stream, error) {
		prompt := r.Messages[0].(llm.UserMessage).Content[0].Text
		answer := assistantMessage(info, llm.StopReasonStop, textPart("done"))
		switch {
		case prompt == "find" && len(r.Messages) == 1:
			answer = assistantMessage(info, llm.StopReasonToolUse, toolCallPart("s", "tool_search", `{}`))
		case prompt == "find" && len(r.Messages) == 3:
			if !hasDefinition(r, "remote") {
				return nil, errors.New("selected tool absent")
			}
			close(selected)
			answer = assistantMessage(info, llm.StopReasonToolUse, toolCallPart("valid", "remote", `{}`))
		case prompt == "skip" && len(r.Messages) == 1:
			select {
			case <-selected:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if hasDefinition(r, "remote") {
				return nil, errors.New("other Run inherited selection")
			}
			answer = assistantMessage(info, llm.StopReasonToolUse, toolCallPart("invalid", "remote", `{}`))
		}
		return &scriptedStream{script: &streamScript{events: terminalEvents(answer)}}, nil
	})
	loop := mustLoop(t, model, []agent.Tool{selectionTool{proposal: []agent.ToolReference{entry.Reference}}})
	errorsOut := make(chan error, 2)
	for _, prompt := range []string{"find", "skip"} {
		input := testInput(info, mustPrompt(t, prompt))
		input.Catalog = catalog
		go func() {
			_, err := loop.Run(ctx, input, nil)
			if err != nil {
				cancel()
			}
			errorsOut <- err
		}()
	}
	for range 2 {
		if err := <-errorsOut; err != nil {
			t.Error(err)
		}
	}
	if len(remote.calls) != 1 || remote.calls[0].ID != "valid" {
		t.Fatal("unselected concurrent call dispatched")
	}
}

type namedSelectionTool struct {
	selectionTool
	name string
}

func (s namedSelectionTool) Definition() llm.ToolDefinition {
	d := s.selectionTool.Definition()
	d.Name = s.name
	return d
}

func TestToolSelectionEvictsWholeDefinitionsAndNotifiesModel(t *testing.T) {
	t.Parallel()
	info := testModel()
	a, b := newFakeTool("remote_a", nil), newFakeTool("remote_b", nil)
	a.definition.Description = strings.Repeat("x", int(agent.ToolSchemaBudget(info))*3)
	b.definition.Description = a.definition.Description
	ea, eb := catalogEntry("a", "v1", a), catalogEntry("b", "v1", b)
	catalog := &selectionCatalog{entries: []agent.CatalogTool{ea, eb}}
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("sa", "search_a", `{}`)))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("sb", "search_b", `{}`)))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("a", "remote_a", `{}`), toolCallPart("b", "remote_b", `{}`)))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	loop := mustLoop(t, model, []agent.Tool{namedSelectionTool{selectionTool: selectionTool{proposal: []agent.ToolReference{ea.Reference}}, name: "search_a"}, namedSelectionTool{selectionTool: selectionTool{proposal: []agent.ToolReference{eb.Reference}}, name: "search_b"}})
	input := testInput(info, mustPrompt(t, "search"))
	input.Catalog = catalog
	_, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := model.requests[2]
	if hasDefinition(request, "remote_a") || !hasDefinition(request, "remote_b") || !strings.Contains(request.SystemPrompt, "evicted") {
		t.Fatal("budget did not evict and notify")
	}
	if len(a.calls) != 0 || len(b.calls) != 1 {
		t.Fatal("evicted tool still executable")
	}
	for _, d := range request.Tools {
		if d.Name == "remote_b" && string(d.InputSchema) != string(b.definition.InputSchema) {
			t.Fatal("schema was truncated")
		}
	}
}
