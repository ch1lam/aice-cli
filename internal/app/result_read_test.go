package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestResultReaderBranchCompactionAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	store := createAppTestSession(t, path, t.TempDir())
	user, err := llm.NewUserMessage(llm.NewTextContent("inspect").Part())
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionMessage(t.Context(), store, user); err != nil {
		t.Fatal(err)
	}
	appendPair := func(callID, text string) string {
		t.Helper()
		assistant := llm.NewAssistantMessage(llm.Model{ID: "fixture", API: "fixture", Provider: "fixture"})
		assistant.StopReason = llm.StopReasonToolUse
		assistant.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: callID, Name: "source", Arguments: json.RawMessage(`{}`)}}}
		result := llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: callID, ToolName: "source", Content: []llm.ContentPart{llm.NewTextContent(text).Part()}, Details: &llm.ToolResultDetails{State: llm.ExecutionUnknown, StructuredContent: json.RawMessage(`{"n":9007199254740993}`)}}
		if err := appendTestSessionMessages(t.Context(), store, []llm.AgentMessage{assistant, result}); err != nil {
			t.Fatal(err)
		}
		id, _ := store.LeafID()
		return id
	}
	first := appendPair("duplicate", "original")
	second := appendPair("duplicate", "second")
	snapshot, _ := store.Snapshot()
	checkpoint, err := session.NewCompaction(session.CompactionInput{ID: "compact", ParentID: second, CreatedAt: 3, Summary: "summarized", TokensBefore: 100, ActiveMessageCount: len(snapshot.Messages)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := withResultReader(t.Context(), store, nil)
	reader := runResultReader{}
	if _, err := reader.ReadToolResult(ctx, "duplicate", ""); err == nil || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) {
		t.Fatal("ambiguous call ID not rejected", err)
	}
	retained, err := reader.ReadToolResult(ctx, "", first)
	if err != nil || !retained.Durable || retained.Message.Content[0].Text != "original" || retained.Message.Details.State != llm.ExecutionUnknown {
		t.Fatal("pre-compaction result unavailable", err)
	}
	retained.Message.Details.StructuredContent[0] = '!'
	retained, err = reader.ReadToolResult(ctx, "", first)
	if err != nil || !json.Valid(retained.Message.Details.StructuredContent) {
		t.Fatal("reader mutated source", err)
	}
	parent, _ := store.LeafID()
	leaf, err := session.NewLeaf("switch", parent, first, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadToolResult(ctx, "", second); err == nil {
		t.Fatal("inactive branch leaked")
	}
	if _, err := reader.ReadToolResult(context.Background(), "", first); err == nil {
		t.Fatal("read without run capability")
	}
}

type resultReadPrintModel struct {
	base     mcpStartupModel
	step     int
	text     string
	durable  bool
	readJSON json.RawMessage
}

func (m *resultReadPrintModel) Stream(ctx context.Context, r llm.Request) (llm.Stream, error) {
	m.step++
	if m.step <= 2 {
		return m.base.Stream(ctx, r)
	}
	last := r.Messages[len(r.Messages)-1].(llm.ToolResultMessage)
	switch m.step {
	case 3:
		if last.ToolCallID != "execute" || last.Details == nil || llm.EstimateMessageTokens(last) > llm.ResultViewBudget(r.Model.ContextWindow) || !strings.Contains(last.Content[len(last.Content)-1].Text, "tool_result_read") {
			m.base.t.Fatal("production request did not trim source")
		}
		m.readJSON = json.RawMessage(strings.Split(last.Content[len(last.Content)-1].Text, "\n")[1])
		if !json.Valid(m.readJSON) {
			m.base.t.Fatal("readback notice did not supply usable JSON arguments")
		}
		args, _ := json.Marshal(map[string]any{"call_id": "execute", "section": "content", "offset": len(m.text) - 12})
		return toolCallEventStream(r.Model, llm.ToolCall{ID: "read-tail", Name: "tool_result_read", Arguments: args}), nil
	case 4:
		if last.IsError || len(last.Content) != 2 || last.Content[1].Text != m.text[len(m.text)-12:] {
			m.base.t.Fatal("tail readback failed", last)
		}
		var meta struct {
			Durable bool `json:"durable"`
		}
		if json.Unmarshal([]byte(last.Content[0].Text), &meta) != nil || meta.Durable != m.durable {
			m.base.t.Fatal("wrong durability claim")
		}
		return toolCallEventStream(r.Model, llm.ToolCall{ID: "read-json", Name: "tool_result_read", Arguments: m.readJSON}), nil
	case 5:
		if last.IsError || len(last.Content) != 2 || last.Content[1].Text != `{"n":9007199254740993}` {
			m.base.t.Fatal("raw JSON readback failed", last)
		}
	}
	return m.base.Stream(ctx, r)
}

func TestMCPPrintResultReadbackRetainsSourceWithoutReplay(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{true: "session", false: "ephemeral"}[durable], func(t *testing.T) {
			text := strings.Repeat("retained source ", 5000) + "final-marker"
			payload, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "structuredContent": json.RawMessage(`{"n":9007199254740993}`)})
			endpoint, _, calls, _ := mcpStartupServer(t, string(payload))
			c := ownerTestConfig(t, 1)
			c, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: endpoint}}})
			if err != nil {
				t.Fatal(err)
			}
			c.DeepSeekAPIKey = "offline-fixture"
			model := &resultReadPrintModel{base: mcpStartupModel{t: t, search: true, wantRemote: true}, text: text, durable: durable}
			command, err := newTestCommand(t, dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil }})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			args := []string{"--workspace", t.TempDir(), "--print", "read retained result", "--yolo"}
			if durable {
				args = append(args, "--session", path)
			}
			command.SetArgs(args)
			command.SetOut(io.Discard)
			var diagnostics bytes.Buffer
			command.SetErr(&diagnostics)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("%v: %s", err, diagnostics.String())
			}
			if model.step != 5 || calls.Load() != 1 {
				t.Fatal("readback repeated RPC or skipped model rounds", model.step, calls.Load())
			}
			if durable {
				store, err := session.Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				retained, err := (runResultReader{}).ReadToolResult(withResultReader(t.Context(), store, nil), "execute", "")
				if err != nil || retained.Message.Content[0].Text != text {
					t.Fatal("Session source was trimmed", err)
				}
			}
		})
	}
}

func TestResultCompactionKeepsUnknownStateAndJSON(t *testing.T) {
	source := llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call", ToolName: "source", Details: &llm.ToolResultDetails{State: llm.ExecutionUnknown, Loss: "binary omitted", StructuredContent: json.RawMessage(`{"n":9007199254740993}`)}}
	text, err := serializeCompactionMessages([]llm.AgentMessage{source})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`state="unknown"`, "binary omitted", `{"n":9007199254740993}`, "tool_result_read"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary source lacks %q", want)
		}
	}
	source.Content = []llm.ContentPart{llm.NewTextContent(strings.Repeat("x", 10000)).Part()}
	text, err = serializeCompactionMessages([]llm.AgentMessage{source})
	if err != nil || !strings.Contains(text, `state="unknown"`) || strings.Contains(text, strings.Repeat("x", 3000)) {
		t.Fatal("long content hid uncertainty or bypassed clipping", err)
	}
}
