package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

type mcpStartupModel struct {
	t          *testing.T
	round      int
	search     bool
	wantRemote bool
	requests   []llm.Request
}

func (m *mcpStartupModel) Stream(_ context.Context, r llm.Request) (llm.Stream, error) {
	m.requests = append(m.requests, r)
	m.round++
	var remote string
	var search bool
	for _, d := range r.Tools {
		if d.Name == "tool_search" {
			search = true
		}
		if strings.HasPrefix(d.Name, "mcp_") {
			remote = d.Name
		}
	}
	if !search || !strings.Contains(r.SystemPrompt, "user:service0") {
		m.t.Fatal("startup omitted MCP discovery or service summary")
	}
	if m.round == 1 && remote != "" {
		m.t.Fatal("previous run selection or eager schema leaked into initial request")
	}
	if m.round == 2 && m.wantRemote != (remote != "") {
		m.t.Fatalf("remote selection = %q, wanted %v", remote, m.wantRemote)
	}
	var call *llm.ToolCall
	if m.round == 1 && m.search {
		call = &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: []byte(`{"service":"user:service0"}`)}
	}
	if m.round == 2 && remote != "" {
		call = &llm.ToolCall{ID: "execute", Name: remote, Arguments: []byte(`{}`)}
	}
	message := llm.NewAssistantMessage(r.Model)
	events := []llm.Event{{Type: llm.EventTypeStart}}
	if call != nil {
		message.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: call}}
		message.StopReason = llm.StopReasonToolUse
		events = append(events, llm.Event{Type: llm.EventTypeToolCallStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeToolCallEnd, ContentIndex: 0, ToolCall: call})
	} else {
		message.Content = []llm.ContentPart{llm.NewTextContent("finished").Part()}
		message.StopReason = llm.StopReasonStop
		events = append(events, llm.Event{Type: llm.EventTypeTextStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "finished"}, llm.Event{Type: llm.EventTypeTextEnd, ContentIndex: 0})
	}
	events = append(events, llm.Event{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message})
	return &eventStream{events: events}, nil
}

func mcpStartupServer(t *testing.T) (string, *atomic.Int32, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var initializes, calls, deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
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
			initializes.Add(1)
			w.Header().Set("Mcp-Session-Id", "startup-fixture")
			result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"read","description":"Read fixture","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			calls.Add(1)
			result = `{"content":[{"type":"text","text":"first"},{"type":"text","text":"last"}],"structuredContent":{"n":9007199254740993}}`
		default:
			t.Errorf("unexpected RPC %q", rpc.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, rpc.ID, result)
	}))
	t.Cleanup(server.Close)
	return server.URL, &initializes, &calls, &deletes
}

func TestMCPPrintCommandLazyDiscoveryAndSession(t *testing.T) {
	for _, mode := range []string{"plain", "unapproved", "approved-tools-ask", "yolo", "denied-yolo"} {
		t.Run(mode, func(t *testing.T) {
			endpoint, initializes, calls, deletes := mcpStartupServer(t)
			c := ownerTestConfig(t, 1)
			var err error
			c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: endpoint}}})
			if err != nil {
				t.Fatal(err)
			}
			c.DeepSeekAPIKey = "offline-fixture"
			c.Provider = string(deepseek.ProviderID)
			c.Model = deepseek.ModelV4Flash
			if mode == "approved-tools-ask" {
				c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
			}
			if mode == "denied-yolo" {
				c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionDeny)
			}
			model := &mcpStartupModel{t: t, search: mode != "plain", wantRemote: mode == "yolo" || mode == "approved-tools-ask"}
			command, err := newTestCommand(t, dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil }})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			args := []string{"--workspace", t.TempDir(), "--session", path, "--print", "inspect"}
			if mode == "yolo" || mode == "denied-yolo" {
				args = append(args, "--yolo")
			}
			command.SetArgs(args)
			var out, diagnostics bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&diagnostics)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("command: %v\n%s", err, diagnostics.String())
			}
			wantConnections := int32(0)
			if model.wantRemote {
				wantConnections = 1
			}
			wantCalls := int32(0)
			if mode == "yolo" {
				wantCalls = 1
			}
			if initializes.Load() != wantConnections || calls.Load() != wantCalls || deletes.Load() != wantConnections {
				t.Fatalf("initialize/call/delete = %d/%d/%d", initializes.Load(), calls.Load(), deletes.Load())
			}
			store, err := session.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			snapshot, err := store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range snapshot.Messages {
				result, ok := entry.Message.(llm.ToolResultMessage)
				if !ok || result.ToolCallID != "execute" {
					continue
				}
				found = true
				if result.Details == nil || result.Details.Binding == nil || result.Details.Binding.ToolName != "read" {
					t.Fatal("CLI lost bound provenance")
				}
				if wantCalls == 1 {
					if result.Details.State != llm.ExecutionReturned || string(result.Details.StructuredContent) != `{"n":9007199254740993}` || len(result.Content) != 2 {
						t.Fatal("CLI source result lost structure/order")
					}
				} else if result.Details.State != llm.ExecutionNotDispatched {
					t.Fatal("connection approval granted tool execution")
				}
			}
			if found != model.wantRemote {
				t.Fatal("Session tool-pair outcome missing")
			}
		})
	}
}

func TestMCPInteractiveCommandReusesTransportNotRunSelection(t *testing.T) {
	endpoint, initializes, calls, deletes := mcpStartupServer(t)
	c := ownerTestConfig(t, 1)
	var err error
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: endpoint}}})
	if err != nil {
		t.Fatal(err)
	}
	c.DeepSeekAPIKey = "offline-fixture"
	model := &mcpStartupModel{t: t, search: true, wantRemote: true}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
		runTUI: func(ctx context.Context, runner interaction.Runner, _ tui.Options) error {
			if initializes.Load() != 0 {
				t.Fatal("interactive startup eagerly connected")
			}
			for range 2 {
				model.round = 0
				run, err := runner.NewRun(ctx, interaction.RunInput{Prompt: "read fixture"}, nil)
				if err != nil {
					return err
				}
				if err := run.Run(ctx); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", t.TempDir(), "--yolo"})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if initializes.Load() != 1 || calls.Load() != 2 || deletes.Load() != 1 {
		t.Fatalf("transport/selection ownership: %d/%d/%d", initializes.Load(), calls.Load(), deletes.Load())
	}
}

func TestMCPRequiredAndPinnedRunPreparation(t *testing.T) {
	for _, mode := range []string{"optional", "required", "pinned", "pin-missing", "required-ask", "required-disabled"} {
		t.Run(mode, func(t *testing.T) {
			c := ownerTestConfig(t, 1)
			server := c.MCP.Servers["user:service0"]
			server.Settings.Required = strings.HasPrefix(mode, "required")
			if strings.HasPrefix(mode, "pin") {
				server.Settings.PinnedTools = []string{"read"}
			}
			if mode == "required-disabled" {
				server.Enabled = false
			}
			c.MCP.Servers[server.Key] = server
			client := &mcpOwnedFixture{}
			if mode != "pin-missing" {
				client.items = []mcpclient.Tool{catalogFixtureTool("read", "read")}
			}
			var opens int
			owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), mode != "required-ask", func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { opens++; return client, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, run, err := owner.bindRun(t.Context())
			if mode == "pin-missing" || mode == "required-ask" || mode == "required-disabled" {
				if err == nil {
					run.Close()
					t.Fatal("unavailable required dependency accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer run.Close()
			if mode == "optional" && opens != 0 || mode != "optional" && opens != 1 {
				t.Fatal("wrong preparation connection behavior")
			}
			if mode == "pinned" && len(run.pins) != 1 {
				t.Fatal("pins not passed to Loop")
			}
		})
	}
}
