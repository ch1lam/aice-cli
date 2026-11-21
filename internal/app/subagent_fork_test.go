package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
)

// forkingModel answers a parent scripted by its request count and a fork,
// recognized by its first message, scripted by its own.
type forkingModel struct {
	mu sync.Mutex
	// routes are the requests' provider routing identities.
	routes   []string
	parents  []llm.Request
	children []llm.Request
	parent   func(m *forkingModel, request llm.Request) llm.Stream
	child    func(request llm.Request, turn int) llm.Stream
}

func isForkRequest(request llm.Request) bool {
	for _, message := range request.Messages {
		if user, ok := message.(llm.UserMessage); ok && strings.HasPrefix(user.Content[0].Text, "[AICE: forked subagent") {
			return true
		}
	}
	return false
}

func (m *forkingModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = append(m.routes, llm.SessionID(ctx))
	if isForkRequest(request) {
		m.children = append(m.children, request)
		return m.child(request, len(m.children)), nil
	}
	m.parents = append(m.parents, request)
	return m.parent(m, request), nil
}

func forkCall(request llm.Request, id, name string, arguments any) llm.Stream {
	data, _ := json.Marshal(arguments)
	return toolCallEventStream(request.Model, llm.ToolCall{ID: id, Name: name, Arguments: data})
}

// A fork's first request is the parent's request that started it, followed
// by the fork's message: same system prompt, tool definitions, model and
// options, and the same history, so a provider can reuse the parent's cache.
func TestForkRepeatsTheParentRequest(t *testing.T) {
	t.Parallel()
	t.Run("without a Session", func(t *testing.T) {
		t.Parallel()
		testForkRepeatsTheParentRequest(t, false)
	})
	// The fork's Session records the parent's request configurations its
	// inherited messages name.
	t.Run("with a Session", func(t *testing.T) {
		t.Parallel()
		testForkRepeatsTheParentRequest(t, true)
	})
}

func testForkRepeatsTheParentRequest(t *testing.T, withSession bool) {
	model := &forkingModel{
		parent: func(m *forkingModel, request llm.Request) llm.Stream {
			switch len(m.parents) {
			case 1:
				return forkCall(request, "look", "ls", map[string]any{"path": "."})
			case 2:
				return forkCall(request, "fork", "agent", map[string]any{"description": "probe", "prompt": "check the listing", "type": "explore", "fork": true, "run_in_background": false})
			}
			return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop)
		},
		child: func(request llm.Request, _ int) llm.Stream {
			return textEventStream(request.Model, "fork report", llm.Usage{}, llm.StopReasonStop)
		},
	}
	cfg := config.Config{APIKeys: map[string]string{"deepseek": "test-key"}, SubagentModel: deepseek.ModelV4Pro}
	workspace := t.TempDir()
	var extra []string
	if withSession {
		extra = []string{"--session", filepath.Join(t.TempDir(), "parent.jsonl")}
	}
	if output := runPrintIn(t, cfg, model, workspace, extra...); !strings.HasSuffix(output, "parent done\n") {
		t.Fatalf("output = %q", output)
	}
	if withSession {
		assertForkSessionRebuilds(t, workspace)
	}
	if len(model.children) != 1 || len(model.parents) != 3 {
		t.Fatalf("requests: %d parent, %d fork", len(model.parents), len(model.children))
	}
	parent, fork := model.parents[1], model.children[0]
	if fork.SystemPrompt != parent.SystemPrompt || !reflect.DeepEqual(fork.Tools, parent.Tools) ||
		fork.Model.ID != parent.Model.ID || !reflect.DeepEqual(fork.Options, parent.Options) {
		t.Fatalf("fork request config differs: model %s/%s, %d/%d tools", fork.Model.ID, parent.Model.ID, len(fork.Tools), len(parent.Tools))
	}
	if len(fork.Messages) != len(parent.Messages)+1 || !reflect.DeepEqual(fork.Messages[:len(parent.Messages)], parent.Messages) {
		t.Fatalf("fork history is not the parent's: %d vs %d messages", len(fork.Messages), len(parent.Messages))
	}
	last, ok := fork.Messages[len(fork.Messages)-1].(llm.UserMessage)
	if !ok || !strings.Contains(last.Content[0].Text, "You change nothing") || !strings.HasSuffix(last.Content[0].Text, "# Task\n\ncheck the listing") {
		t.Fatalf("fork message = %#v", fork.Messages[len(fork.Messages)-1])
	}
	// Requests: parent, parent, fork, parent.
	if model.routes[2] == "" || model.routes[2] != model.routes[1] {
		t.Fatalf("fork routes as %q, parent as %q", model.routes[2], model.routes[1])
	}
	if result := model.parents[2].Messages[len(model.parents[2].Messages)-1].(llm.ToolResultMessage); !strings.Contains(result.Content[0].Text, "fork report") {
		t.Fatalf("parent saw %q", result.Content[0].Text)
	}
}

// A background fork continues with send_message from its whole Session:
// the inherited conversation, its first turn and the message.
func TestBackgroundForkContinues(t *testing.T) {
	t.Parallel()
	var id string
	model := &forkingModel{
		parent: func(m *forkingModel, request llm.Request) llm.Stream {
			last := request.Messages[len(request.Messages)-1]
			if result, ok := last.(llm.ToolResultMessage); ok && result.ToolName == "agent" {
				id = taskIDPattern.FindString(result.Content[0].Text)
			}
			switch notice := noticeText(last); {
			case len(m.parents) == 1:
				return forkCall(request, "fork", "agent", map[string]any{"description": "probe", "prompt": "first part", "type": "explore", "fork": true})
			case strings.Contains(notice, "second report"):
				return textEventStream(request.Model, "parent done", llm.Usage{}, llm.StopReasonStop)
			case strings.Contains(notice, "first report"):
				return forkCall(request, "more", "send_message", map[string]any{"to": id, "message": "second part"})
			}
			return textEventStream(request.Model, "waiting", llm.Usage{}, llm.StopReasonStop)
		},
		child: func(request llm.Request, turn int) llm.Stream {
			return textEventStream(request.Model, map[int]string{1: "first report", 2: "second report"}[turn], llm.Usage{}, llm.StopReasonStop)
		},
	}
	if output := runPrintWith(t, model); !strings.HasSuffix(output, "parent done\n") {
		t.Fatalf("output = %q", output)
	}
	if len(model.children) != 2 {
		t.Fatalf("fork requests = %d", len(model.children))
	}
	first, second := model.children[0], model.children[1]
	if !reflect.DeepEqual(second.Messages[:len(first.Messages)], first.Messages) {
		t.Fatal("the continued fork lost its inherited conversation")
	}
	if texts := userTexts(second); texts[len(texts)-1] != "second part" {
		t.Fatalf("continued fork users = %q", texts)
	}
}

// A fork keeps the parent's definitions in order; a tool it may not use
// fails, and its bash refuses only the background.
func TestForkToolsKeepParentDefinitions(t *testing.T) {
	t.Parallel()
	parentBash := &stubTool{definition: llm.ToolDefinition{Name: "bash", Description: "run", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"run_in_background":{"type":"boolean"}}}`)}}
	childBash := &stubTool{definition: llm.ToolDefinition{Name: "bash", Description: "run", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)}}
	read := &stubTool{definition: llm.ToolDefinition{Name: "read", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	delegate := &stubTool{definition: llm.ToolDefinition{Name: "agent", Description: "delegate", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	tools := forkTools([]agent.Tool{read, delegate, parentBash}, []agent.Tool{childBash, read})
	for index, want := range []agent.Tool{read, delegate, parentBash} {
		if !reflect.DeepEqual(tools[index].Definition(), want.Definition()) {
			t.Fatalf("tool %d = %s", index, tools[index].Definition().Name)
		}
	}
	if tools[0] != agent.Tool(read) {
		t.Fatal("a tool with the parent's definition was wrapped")
	}
	if _, err := tools[1].Execute(t.Context(), llm.ToolCall{Name: "agent"}); err == nil {
		t.Fatal("a fork may delegate")
	}
	if _, err := tools[2].Execute(t.Context(), llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"make","run_in_background":true}`)}); err == nil {
		t.Fatal("a fork started a background task")
	}
	if _, err := tools[2].Execute(t.Context(), llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"make","run_in_background":false}`)}); err != nil ||
		string(childBash.calls[0].Arguments) != `{"command":"make"}` {
		t.Fatalf("foreground bash = %v, %s", err, childBash.calls)
	}
}

// A compaction summary in a fork's conversation becomes the user message
// every request shows for it, which a Session can record.
func TestForkProjectsCompactionSummary(t *testing.T) {
	t.Parallel()
	summary, err := llm.NewCompactionSummaryMessage("earlier work", 100)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent("go on").Part())
	if err != nil {
		t.Fatal(err)
	}
	history, err := projectSummaries([]llm.AgentMessage{summary, prompt})
	if err != nil {
		t.Fatal(err)
	}
	want, err := llm.AgentMessagesToMessages([]llm.AgentMessage{summary, prompt})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := llm.AgentMessagesToMessages(history)
	if err != nil || !reflect.DeepEqual(projected, want) {
		t.Fatalf("projection changed: %v", err)
	}
	if _, err := session.NewMessage("m1", "", 1, history[0]); err != nil {
		t.Fatalf("a Session cannot record the projected summary: %v", err)
	}
}

type stubTool struct {
	definition llm.ToolDefinition
	calls      []llm.ToolCall
}

func (s *stubTool) Definition() llm.ToolDefinition { return s.definition }

func (s *stubTool) Execute(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	s.calls = append(s.calls, call)
	return llm.ToolResult{CallID: call.ID, Name: call.Name, Content: []llm.ContentPart{llm.NewTextContent("ok").Part()}}, nil
}

// Auto mode reads a fork's inherited conversation once, from the parent's
// branch: the fork's copy of it is skipped.
func TestForkTranscriptSkipsInheritedEntries(t *testing.T) {
	t.Parallel()
	user, err := llm.NewUserMessage(llm.NewTextContent("fix the flaky test").Part())
	if err != nil {
		t.Fatal(err)
	}
	inherited := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent("parent step").Part()}, StopReason: llm.StopReasonStop}
	own := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent("fork step").Part()}, StopReason: llm.StopReasonStop}
	parent := runTranscript(func() ([]session.MessageEntry, error) {
		return []session.MessageEntry{{ID: "p1", Message: user}, {ID: "p2", Message: inherited}}, nil
	})
	memory := []llm.AgentMessage{user, inherited, own}
	entries, err := childTranscript(parent, nil, func() []llm.AgentMessage { return memory }, 2)()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[2].Message.(llm.AssistantMessage).Content[0].Text != "fork step" {
		t.Fatalf("entries = %#v", entries)
	}
}

// assertForkSessionRebuilds checks that the fork's Session can rebuild the
// request of every assistant message, inherited ones included.
func assertForkSessionRebuilds(t *testing.T, workspace string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(workspace, ".aice", "sessions", "agents", "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("child Sessions = %v, %v", paths, err)
	}
	store, err := session.Open(t.Context(), paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	assistants := 0
	for _, entry := range snapshot.Messages {
		if _, ok := entry.Message.(llm.AssistantMessage); !ok {
			continue
		}
		assistants++
		if _, err := session.RebuildRequest(snapshot, entry.ID); err != nil {
			t.Fatalf("rebuild %s: %v", entry.ID, err)
		}
	}
	// The parent's ls call and the fork's report.
	if assistants != 2 {
		t.Fatalf("child Session has %d assistant messages", assistants)
	}
}
