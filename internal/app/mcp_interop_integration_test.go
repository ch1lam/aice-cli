//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestMCPPrintInteropHarness(t *testing.T) {
	endpoint, initializes, calls, closes := mcpStartupServer(t)
	definition := config.MCPServerSettings{Transport: "http", URL: endpoint, IncludeTools: &[]string{"read"}}
	mcpPrintInterop(t, definition, "read", "read", json.RawMessage(`{}`), "firstlast")
	if initializes.Load() != 2 || calls.Load() != 1 || closes.Load() != 2 {
		t.Fatal("inspection/Print lifecycle or single-dispatch contract changed", initializes.Load(), calls.Load(), closes.Load())
	}
}

func TestMCPInteropModelSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expected string
		loaded         bool
		wantError      bool
	}{
		{"expected candidate is second", "read", true, false},
		{"expected candidate absent", "missing", true, true},
		{"expected schema absent", "read", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovery := tool.ToolSearchResult{
				Services: []tool.MCPInfoView{{Service: "user:interop"}},
				Entries: []tool.ToolSearchEntry{
					{ID: mcpToolID("user:interop", "wrong"), Name: "wrong_schema"},
					{ID: mcpToolID("user:interop", "read"), Name: "read_schema"},
				},
			}
			encoded, err := json.Marshal(discovery)
			if err != nil {
				t.Fatal(err)
			}
			request := llm.Request{Tools: []llm.ToolDefinition{{Name: "wrong_schema"}}, Messages: []llm.Message{
				llm.ToolResultMessage{ToolCallID: "interop-discover", Content: []llm.ContentPart{llm.NewTextContent(string(encoded)).Part()}},
			}}
			if tc.loaded {
				request.Tools = append(request.Tools, llm.ToolDefinition{Name: "read_schema"})
			}
			model := &mcpInteropModel{round: 1, remote: tc.expected, arguments: json.RawMessage(`{}`)}
			stream, err := model.Stream(t.Context(), request)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected selection result: %v", err)
			}
			if err != nil {
				return
			}
			defer stream.Close()
			for {
				event, err := stream.Next()
				if err != nil {
					t.Fatal("selected call missing", err)
				}
				if event.Type == llm.EventTypeToolCallEnd {
					if event.ToolCall == nil || event.ToolCall.Name != "read_schema" || model.candidates != 2 {
						t.Fatal("interop model chose another discovery candidate")
					}
					break
				}
			}
		})
	}
}

// Explicit paths select an already installed server. This test never installs
// packages, reads provider credentials or gives the server user/workspace files.
func TestMCPFilesystemPrintInterop(t *testing.T) {
	if os.Getenv("AICE_MCP_FILESYSTEM_TEST") != "1" {
		t.Skip("set AICE_MCP_FILESYSTEM_TEST=1 and supply Node/server paths")
	}
	node, entry := os.Getenv("AICE_MCP_TEST_NODE"), os.Getenv("AICE_MCP_FILESYSTEM_ENTRY")
	for _, path := range []string{node, entry} {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
			t.Fatal("Node and filesystem entry must be existing absolute file paths")
		}
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(entry)), "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Name, Version string }
	if json.Unmarshal(data, &pkg) != nil || pkg.Name != "@modelcontextprotocol/server-filesystem" || pkg.Version != "2026.8.31" {
		t.Fatal("expected the reviewed filesystem package version 2026.8.31")
	}
	root := t.TempDir()
	file := filepath.Join(root, "fixture.txt")
	const content = "AICE stdio interoperability fixture\n中文内容 · exact readback\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := config.MCPServerSettings{Transport: "stdio", Command: node, Args: []string{entry, root}, Cwd: root}
	args, err := json.Marshal(map[string]string{"path": file})
	if err != nil {
		t.Fatal(err)
	}
	mcpPrintInterop(t, definition, "read_text_file", "read text file", args, content)
	unchanged, err := os.ReadFile(file)
	if err != nil || string(unchanged) != content {
		t.Fatal("read-only interoperability changed the synthetic file")
	}
}

// Only this fixed public repository identifier leaves the process. The model
// is scripted; there is no provider traffic or real account credential lookup.
func TestMCPDeepWikiPrintInterop(t *testing.T) {
	if os.Getenv("AICE_MCP_DEEPWIKI_TEST") != "1" {
		t.Skip("set AICE_MCP_DEEPWIKI_TEST=1 for public remote read interoperability")
	}
	definition := config.MCPServerSettings{Transport: "http", URL: "https://mcp.deepwiki.com/mcp", CallTimeout: "45s"}
	mcpPrintInterop(t, definition, "read_wiki_structure", "repository documentation topics", json.RawMessage(`{"repoName":"facebook/react"}`), "")
}

func mcpPrintInterop(t *testing.T, definition config.MCPServerSettings, remote, query string, arguments json.RawMessage, exactText string) {
	t.Helper()
	paths := trustTestPaths(t)
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := managementCommand(t, paths, string(encoded), "add", "interop"); err != nil || !result.Committed {
		t.Fatalf("save configuration: %v", err)
	}
	status, err := managementCommand(t, paths, "", "status", "user:interop")
	if err != nil || len(status.Services) != 1 {
		t.Fatalf("inspect identity: %v", err)
	}
	binding := status.Services[0]
	if _, err := managementCommand(t, paths, "", "approve", binding.Key, "--fingerprint", binding.Fingerprint); err != nil {
		t.Fatal(err)
	}
	permissions, err := managementCommand(t, paths, "", "permissions", binding.Key)
	if err != nil {
		t.Fatal(err)
	}
	schema := ""
	eligibleTools := 0
	for _, permission := range permissions.Permissions {
		if permission.Operation == "" && permission.Eligible {
			eligibleTools++
		}
		if permission.Tool == remote && permission.Operation == "" {
			schema = permission.SchemaFingerprint
		}
	}
	if schema == "" {
		t.Fatal("reviewed read tool absent from real service catalog")
	}
	if result, err := managementCommand(t, paths, "", "permission", binding.Key, "--fingerprint", binding.Fingerprint, "--scope", binding.PermissionScope, "--tool", remote, "--schema-fingerprint", schema, "--decision", "allow"); err != nil || !result.Committed {
		t.Fatalf("save exact read permission: %v", err)
	}
	model := &mcpInteropModel{query: query, remote: remote, arguments: arguments}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) {
			c, err := config.LoadFiles(paths, options)
			c.DeepSeekAPIKey = "offline-fixture"
			return c, err
		},
		newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	command.SetArgs([]string{"--workspace", t.TempDir(), "--no-dep-install", "--no-update-check", "--session", path, "--print", "read the synthetic or public interoperability fixture"})
	command.SetOut(io.Discard)
	var diagnostics bytes.Buffer
	command.SetErr(&diagnostics)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("Print: %v\n%s", err, diagnostics.String())
	}
	t.Logf("server=%q version=%q protocol=%q", model.info.Name, model.info.Version, model.info.Protocol)
	t.Logf("eligible_tools=%d selected_candidates=%d expected_tool=%s", eligibleTools, model.candidates, remote)
	if model.round != 3 {
		t.Fatalf("unexpected scripted request count %d", model.round)
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
	calls := 0
	for _, entry := range snapshot.Messages {
		result, ok := entry.Message.(llm.ToolResultMessage)
		if !ok || result.ToolCallID != "interop-read" {
			continue
		}
		calls++
		if result.IsError || result.Details == nil || result.Details.State != llm.ExecutionReturned || result.Details.Binding == nil || result.Details.Binding.ToolName != remote || result.Details.Binding.ConnectionFingerprint != binding.Fingerprint || len(result.Content) == 0 {
			t.Fatal("Session lost successful result or configured identity")
		}
		var text strings.Builder
		for _, part := range result.Content {
			text.WriteString(part.Text)
		}
		if text.Len() == 0 || exactText != "" && text.String() != exactText {
			t.Fatal("Session readback differs from expected real service content")
		}
		t.Logf("transport=%s tool=%s scripted_requests=%d persisted_calls=%d result_blocks=%d text_bytes=%d exact_fixture=%t", definition.Transport, remote, model.round, calls, len(result.Content), text.Len(), exactText != "")
	}
	if calls != 1 {
		t.Fatalf("expected one persisted read, got %d", calls)
	}
}

type mcpInteropModel struct {
	round      int
	info       tool.MCPInfoView
	query      string
	remote     string
	candidates int
	arguments  json.RawMessage
}

func (m *mcpInteropModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	m.round++
	var call *llm.ToolCall
	switch m.round {
	case 1:
		for _, d := range request.Tools {
			if strings.HasPrefix(d.Name, "mcp_") && d.Name != "mcp_resource_list" && d.Name != "mcp_server_info" {
				return nil, fmt.Errorf("eager remote schema in initial request")
			}
		}
		args, err := json.Marshal(tool.ToolSearchRequest{Query: m.query, Service: "user:interop", Limit: 5})
		if err != nil {
			return nil, err
		}
		call = &llm.ToolCall{ID: "interop-discover", Name: "tool_search", Arguments: args}
	case 2:
		for _, message := range request.Messages {
			result, ok := message.(llm.ToolResultMessage)
			if !ok || result.ToolCallID != "interop-discover" || result.IsError || len(result.Content) != 1 {
				continue
			}
			var discovery tool.ToolSearchResult
			if json.Unmarshal([]byte(result.Content[0].Text), &discovery) != nil || len(discovery.Entries) < 1 || len(discovery.Entries) > 5 {
				return nil, fmt.Errorf("expected a bounded set of discovery candidates")
			}
			if len(discovery.Services) != 1 || discovery.Services[0].Service != "user:interop" {
				return nil, fmt.Errorf("expected configured service metadata")
			}
			m.info = discovery.Services[0]
			m.candidates = len(discovery.Entries)
			for _, candidate := range discovery.Entries {
				if candidate.ID != mcpToolID("user:interop", m.remote) {
					continue
				}
				for _, d := range request.Tools {
					if d.Name == candidate.Name {
						call = &llm.ToolCall{ID: "interop-read", Name: d.Name, Arguments: m.arguments}
					}
				}
			}
		}
		if call == nil {
			return nil, fmt.Errorf("discovered read Schema not loaded on next request")
		}
	case 3:
		found := false
		for _, message := range request.Messages {
			result, ok := message.(llm.ToolResultMessage)
			if ok && result.ToolCallID == "interop-read" {
				found = !result.IsError && result.Details != nil && result.Details.State == llm.ExecutionReturned
			}
		}
		if !found {
			return nil, fmt.Errorf("model did not receive successful read result")
		}
	default:
		return nil, fmt.Errorf("unexpected request or retry in interoperability fixture")
	}
	message := llm.NewAssistantMessage(request.Model)
	events := []llm.Event{{Type: llm.EventTypeStart}}
	if call != nil {
		message.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: call}}
		message.StopReason = llm.StopReasonToolUse
		events = append(events, llm.Event{Type: llm.EventTypeToolCallStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeToolCallEnd, ContentIndex: 0, ToolCall: call})
	} else {
		message.Content = []llm.ContentPart{llm.NewTextContent("interop read complete").Part()}
		message.StopReason = llm.StopReasonStop
		events = append(events, llm.Event{Type: llm.EventTypeTextStart, ContentIndex: 0}, llm.Event{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "interop read complete"}, llm.Event{Type: llm.EventTypeTextEnd, ContentIndex: 0})
	}
	events = append(events, llm.Event{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message})
	return &eventStream{events: events}, nil
}
