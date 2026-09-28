package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type resourceCatalogFixture struct {
	mcpCatalogFixture
	resourceGeneration atomic.Uint64
	beforeRead         func()
}

func (f *resourceCatalogFixture) Resources(context.Context) (mcpclient.Catalog[mcpclient.Resource], error) {
	return mcpclient.Catalog[mcpclient.Resource]{Items: []mcpclient.Resource{{URI: "fixture://one", Name: "one"}, {URI: "fixture://two", Name: "two"}}, Generation: f.resourceGeneration.Load(), Complete: true}, nil
}
func (f *resourceCatalogFixture) ResourceGeneration() uint64 { return f.resourceGeneration.Load() }
func (f *resourceCatalogFixture) ReadResourceChecked(ctx context.Context, _ string, check func(context.Context) error) (mcpclient.Result, error) {
	if f.beforeRead != nil {
		f.beforeRead()
	}
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	f.calls.Add(1)
	return mcpclient.Result{State: llm.ExecutionReturned}, nil
}
func TestMCPResourceAndToolCatalogsRemainIndependent(t *testing.T) {
	f := &resourceCatalogFixture{mcpCatalogFixture: mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("resources/read", "ordinary tool")}}}
	gate, _ := guard.New("", guard.Config{})
	catalog, err := newMCPCatalog(catalogTestConfig("user:docs"), map[string]mcpCatalogConnection{"user:docs": f}, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	resources, err := catalog.ListResources(t.Context(), tool.ResourceListRequest{Service: "user:docs", Limit: 5})
	if err != nil || len(resources.Selected) != 1 {
		t.Fatal(resources, err)
	}
	tools, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Service: "user:docs", Limit: 5})
	if err != nil || len(tools.Selected) != 1 || resources.ReadTool == tools.Entries[0].Name {
		t.Fatal("resource/tool collision", err)
	}
	if err := catalog.Check(t.Context(), resources.Selected[0]); err != nil {
		t.Fatal("tool refresh removed resource", err)
	}
	f.generation.Add(1)
	if catalog.Check(t.Context(), tools.Selected[0]) == nil {
		t.Fatal("tool notification ignored")
	}
	if err := catalog.Check(t.Context(), resources.Selected[0]); err != nil {
		t.Fatal("tool notification invalidated resource", err)
	}
	f.resourceGeneration.Add(1)
	if catalog.Check(t.Context(), resources.Selected[0]) == nil {
		t.Fatal("resource notification ignored")
	}
	resolved, _ := catalog.Resolve(t.Context(), []string{resources.Selected[0].ID})
	if len(resolved) != 0 {
		t.Fatal("stale resource resolved")
	}
	if f.calls.Load() != 0 {
		t.Fatal("discovery read a resource")
	}
}

type resourcePrintModel struct {
	t                    *testing.T
	round                int
	wantReader, wantRead bool
	reader               string
}

func (m *resourcePrintModel) Stream(_ context.Context, r llm.Request) (llm.Stream, error) {
	m.round++
	for _, d := range r.Tools {
		if strings.HasPrefix(d.Name, "mcp_resource_read_") {
			m.reader = d.Name
		}
	}
	switch m.round {
	case 1:
		if m.reader != "" {
			m.t.Fatal("resource reader eagerly loaded")
		}
		return toolCallEventStream(r.Model, llm.ToolCall{ID: "list", Name: "mcp_resource_list", Arguments: json.RawMessage(`{"service":"user:service0","limit":1}`)}), nil
	case 2:
		if (m.reader != "") != m.wantReader {
			m.t.Fatal("wrong reader selection", m.reader)
		}
		if m.wantReader {
			last := r.Messages[len(r.Messages)-1].(llm.ToolResultMessage)
			if !strings.Contains(last.Content[0].Text, `"next_offset":1`) {
				m.t.Fatal("missing resource page cursor")
			}
			return toolCallEventStream(r.Model, llm.ToolCall{ID: "read-resource", Name: m.reader, Arguments: json.RawMessage(`{"uri":"fixture://one"}`)}), nil
		}
	case 3:
		result := r.Messages[len(r.Messages)-1].(llm.ToolResultMessage)
		if result.Details == nil || result.Details.Binding == nil || result.Details.Binding.Operation != llm.OperationResourceRead {
			m.t.Fatal("resource provenance lost")
		}
		if m.wantRead {
			if result.IsError || result.Details.State != llm.ExecutionReturned || len(result.Content) != 3 || result.Content[1].Type != llm.ContentTypeImage || !strings.Contains(result.Content[0].Text, "before") || !strings.Contains(result.Content[2].Text, "after") {
				m.t.Fatal("ordered resource result lost", result)
			}
		} else if !result.IsError || result.Details.State != llm.ExecutionNotDispatched {
			m.t.Fatal("connection approval authorized read")
		}
	}
	message := llm.NewAssistantMessage(r.Model)
	message.Content = []llm.ContentPart{llm.NewTextContent("finished").Part()}
	message.StopReason = llm.StopReasonStop
	return &eventStream{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeTextStart, ContentIndex: 0}, {Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "finished"}, {Type: llm.EventTypeTextEnd, ContentIndex: 0}, {Type: llm.EventTypeDone, StopReason: llm.StopReasonStop, Message: &message}}}, nil
}

func TestMCPResourcesThroughPrintAndSession(t *testing.T) {
	for _, mode := range []string{"unapproved", "connection-only", "user-allow", "user-deny-yolo", "yolo", "denied-yolo", "required-resource-only"} {
		t.Run(mode, func(t *testing.T) {
			var initializes, lists, reads atomic.Int32
			var picture bytes.Buffer
			if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
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
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(request.ID) == 0 {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				result := `{}`
				switch request.Method {
				case "initialize":
					initializes.Add(1)
					result = `{"protocolVersion":"2025-11-25","capabilities":{"resources":{}},"serverInfo":{"name":"resource-only","version":"1"}}`
				case "resources/list":
					lists.Add(1)
					result = `{"resources":[{"uri":"fixture://one","name":"one"},{"uri":"fixture://two","name":"two"}]}`
				case "resources/read":
					reads.Add(1)
					if string(request.Params) != `{"uri":"fixture://one"}` {
						t.Errorf("URI changed: %s", request.Params)
					}
					result = `{"contents":[{"uri":"fixture://one","text":"before","mimeType":"text/plain"},{"uri":"fixture://one","blob":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `","mimeType":"image/png"},{"uri":"fixture://one","text":"after; link https://unfetched.invalid/","mimeType":"text/plain"}]}`
				default:
					t.Errorf("unexpected RPC %q", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)
			}))
			defer server.Close()
			c := ownerTestConfig(t, 1)
			c, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: server.URL, Required: mode == "required-resource-only"}}})
			if err != nil {
				t.Fatal(err)
			}
			c.DeepSeekAPIKey = "offline-fixture"
			if mode == "connection-only" || mode == "user-allow" || mode == "user-deny-yolo" {
				c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
			}
			if mode == "denied-yolo" {
				c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionDeny)
			}
			if mode == "user-allow" || mode == "user-deny-yolo" {
				decision := "allow"
				if mode == "user-deny-yolo" {
					decision = "deny"
				}
				c, err = c.WithMCPPermission("user:service0", c.MCP.Servers["user:service0"].Fingerprint, config.MCPPermission{Operation: llm.OperationResourceRead, Tool: llm.OperationResourceRead, SchemaFingerprint: mcpDigest(mcpResourceReadSchema), Decision: decision})
				if err != nil {
					t.Fatal(err)
				}
			}
			model := &resourcePrintModel{t: t, wantReader: mode == "user-allow" || mode == "connection-only" || mode == "yolo" || mode == "required-resource-only", wantRead: mode == "user-allow" || mode == "yolo" || mode == "required-resource-only"}
			command, err := newTestCommand(t, dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil }})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "resource.jsonl")
			args := []string{"--workspace", t.TempDir(), "--session", path, "--print", "read resources"}
			if mode == "yolo" || mode == "user-deny-yolo" || mode == "denied-yolo" || mode == "required-resource-only" {
				args = append(args, "--yolo")
			}
			command.SetArgs(args)
			command.SetOut(io.Discard)
			var diagnostics bytes.Buffer
			command.SetErr(&diagnostics)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("%v: %s", err, diagnostics.String())
			}
			want := int32(0)
			if model.wantRead {
				want = 1
			}
			if reads.Load() != want {
				t.Fatal("unexpected resource dispatch", reads.Load())
			}
			if (mode == "unapproved" || mode == "denied-yolo") && (initializes.Load() != 0 || lists.Load() != 0) {
				t.Fatal("unapproved service contacted")
			}
			if mode == "connection-only" {
				management, err := newTestCommand(t, dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) {
					t.Error("management created model")
					return nil, fmt.Errorf("unexpected model")
				}})
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				management.SetArgs([]string{"mcp", "connect", "user:service0", "--workspace", t.TempDir()})
				management.SetOut(&output)
				management.SetErr(io.Discard)
				if err := management.ExecuteContext(t.Context()); err != nil {
					t.Fatal("resource-only connection test failed", err)
				}
				if !strings.Contains(output.String(), "resource discovery succeeded") || reads.Load() != 0 {
					t.Fatal("management read a resource or misreported discovery", output.String())
				}
			}
			if model.wantReader {
				store, err := session.Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				retained, err := (runResultReader{}).ReadToolResult(withResultReader(t.Context(), store, nil), "read-resource", "")
				if err != nil || retained.Message.Details.Binding.Operation != llm.OperationResourceRead {
					t.Fatal("resource Session replay lost operation", err)
				}
			}
		})
	}
}

func TestMCPResourceReadRechecksLoopPermissionAtDispatch(t *testing.T) {
	gate, _ := guard.New("", guard.Config{})
	client := &resourceCatalogFixture{beforeRead: gate.ResetSessionGrants}
	catalog, err := newMCPCatalog(catalogTestConfig("user:service0"), map[string]mcpCatalogConnection{"user:service0": client}, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	list, err := tool.NewMCPResourceList(catalog)
	if err != nil {
		t.Fatal(err)
	}
	model := &resourcePrintModel{t: t, wantReader: true}
	loop, err := agent.NewLoop(model, []agent.Tool{list}, agent.WithGuard(&guardAdapter{inner: gate, mcp: catalog, yolo: true}))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := llm.NewUserMessage(llm.NewTextContent("read fixture").Part())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Run(t.Context(), agent.RunInput{Model: deepseek.DefaultModel(), Catalog: catalog, Prompt: prompt}, nil); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 0 || model.round != 3 {
		t.Fatal("stale read permission dispatched", client.calls.Load(), model.round)
	}
}
