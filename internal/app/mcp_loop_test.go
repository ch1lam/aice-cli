package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpSearchLoopModel struct {
	t        *testing.T
	round    int
	selected string
}

type mcpDispatchFixture struct {
	mcpCatalogConnection
	once   sync.Once
	before func()
}

func (f *mcpDispatchFixture) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	f.once.Do(f.before)
	return f.mcpCatalogConnection.CallChecked(ctx, name, args, check)
}

func (m *mcpSearchLoopModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.round++
	message := llm.NewAssistantMessage(request.Model)
	var call *llm.ToolCall
	switch m.round {
	case 1:
		if len(request.Tools) != 1 || request.Tools[0].Name != "tool_search" {
			m.t.Fatalf("eager schema injection: %+v", request.Tools)
		}
		call = &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: []byte(`{"query":"read","limit":1}`)}
	case 2:
		for _, definition := range request.Tools {
			if strings.HasPrefix(definition.Name, "mcp_") {
				m.selected = definition.Name
			}
		}
		if m.selected == "" || len(request.Tools) != 2 {
			m.t.Fatalf("search did not select next-round definition: %+v", request.Tools)
		}
		call = &llm.ToolCall{ID: "execute", Name: m.selected, Arguments: []byte(`{"n":9007199254740993}`)}
	default:
		message.Content = []llm.ContentPart{llm.NewTextContent("finished").Part()}
		message.StopReason = llm.StopReasonStop
	}
	events := []llm.Event{{Type: llm.EventTypeStart}}
	if call != nil {
		message.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: call}}
		message.StopReason = llm.StopReasonToolUse
		events = append(events, llm.Event{Type: llm.EventTypeToolCallStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeToolCallEnd, ContentIndex: 0, ToolCall: call})
	} else {
		events = append(events, llm.Event{Type: llm.EventTypeTextStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "finished"}, llm.Event{Type: llm.EventTypeTextEnd, ContentIndex: 0})
	}
	events = append(events, llm.Event{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message})
	return &eventStream{events: events}, nil
}

func TestMCPSearchLoopHTTPAndSession(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		yolo, resetAtDispatch bool
	}{{"ask", false, false}, {"yolo", true, false}, {"reset-after-approval", true, true}} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var imageBytes bytes.Buffer
			if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var rpc struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&rpc) != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(rpc.ID) == 0 {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				result := `{}`
				switch rpc.Method {
				case "initialize":
					result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"local-fixture","version":"1"}}`
					w.Header().Set("Mcp-Session-Id", "test-session")
				case "tools/list":
					result = `{"tools":[{"name":"read_original","description":"read fixture","inputSchema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}]}`
				case "tools/call":
					calls.Add(1)
					var params struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					}
					_ = json.Unmarshal(rpc.Params, &params)
					if params.Name != "read_original" || !bytes.Contains(params.Arguments, []byte("9007199254740993")) {
						t.Errorf("wrong remote call: %s", rpc.Params)
					}
					result = `{"content":[{"type":"text","text":"before"},{"type":"image","mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString(imageBytes.Bytes()) + `"},{"type":"text","text":"after"}],"structuredContent":{"n":9007199254740993},"isError":true}`
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, rpc.ID, result)
			}))
			defer server.Close()
			client, err := mcpclient.Open(t.Context(), mcpclient.Config{HTTP: &mcpclient.HTTPConfig{Endpoint: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			gate, _ := guard.New(t.TempDir(), guard.Config{})
			var connection mcpCatalogConnection = client
			if test.resetAtDispatch {
				connection = &mcpDispatchFixture{mcpCatalogConnection: client, before: gate.ResetSessionGrants}
			}
			catalog, err := newMCPCatalog(catalogTestConfig("user:fixture"), map[string]mcpCatalogConnection{"user:fixture": connection}, gate)
			if err != nil {
				t.Fatal(err)
			}
			defer catalog.Close()
			search, _ := tool.NewToolSearch(catalog)
			model := &mcpSearchLoopModel{t: t}
			loop, err := agent.NewLoop(model, []agent.Tool{search}, agent.WithGuard(&guardAdapter{inner: gate, mcp: catalog, yolo: test.yolo}))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "mcp.jsonl")
			store, err := session.Create(t.Context(), path, session.Metadata{ID: "mcp-test", CreatedAt: 1, WorkingDirectory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			counter := 0
			record := func(ctx context.Context, message llm.AgentMessage) error {
				counter++
				parent, err := store.LeafID()
				if err != nil {
					return err
				}
				entry, err := session.NewMessage(fmt.Sprintf("message-%d", counter), parent, int64(counter), message)
				if err != nil {
					return err
				}
				return store.AppendMessage(ctx, entry)
			}
			prompt, _ := llm.NewUserMessage(llm.NewTextContent("read fixture through discovery").Part())
			result, err := loop.Run(t.Context(), agent.RunInput{Model: deepseek.DefaultModel(), Catalog: catalog, Prompt: prompt, MessageRecorder: record}, nil)
			if err != nil || model.round != 3 {
				t.Fatalf("loop=%+v %v", result, err)
			}
			var outcome llm.ToolResultMessage
			for _, message := range result.Messages() {
				if toolResult, ok := message.(llm.ToolResultMessage); ok && toolResult.ToolCallID == "execute" {
					outcome = toolResult
				}
			}
			if outcome.Details == nil || outcome.Details.Binding == nil || outcome.Details.Binding.ToolName != "read_original" {
				t.Fatalf("missing provenance: %+v", outcome)
			}
			if test.yolo && !test.resetAtDispatch {
				if calls.Load() != 1 || outcome.Details.State != llm.ExecutionReturned || !outcome.IsError || len(outcome.Content) != 3 || outcome.Content[0].Text != "before" || outcome.Content[1].Image == nil || outcome.Content[2].Text != "after" || string(outcome.Details.StructuredContent) != `{"n":9007199254740993}` {
					t.Fatalf("lost ordered partial result: %+v calls=%d", outcome, calls.Load())
				}
			} else if calls.Load() != 0 || outcome.Details.State != llm.ExecutionNotDispatched || !outcome.IsError {
				t.Fatalf("noninteractive ask dispatched: %+v calls=%d", outcome, calls.Load())
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := session.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range snapshot.Messages {
				if saved, ok := entry.Message.(llm.ToolResultMessage); ok && saved.ToolCallID == "execute" {
					found = true
					if !reflect.DeepEqual(saved, outcome) {
						t.Fatal("Session reopen changed source result")
					}
				}
			}
			if !found {
				t.Fatal("result missing after reopen")
			}
			// A fresh run still starts with only search, despite the previous run's
			// catalog and durable history. The model checks that first request again.
			model.round, model.selected = 0, ""
			if _, err := loop.Run(t.Context(), agent.RunInput{Model: deepseek.DefaultModel(), Catalog: catalog, Prompt: prompt}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
