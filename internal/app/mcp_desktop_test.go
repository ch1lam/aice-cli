package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func managedCatalogFixture(t *testing.T, configuration config.MCPConfig) (*mcpCatalog, *guard.Guard, *mcpCatalogFixture) {
	t.Helper()
	backend := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("list_windows", "Discover windows"), catalogFixtureTool("click", "Click a discovered window"), catalogFixtureTool("future_input", "Unreviewed future capability")}}
	gate, err := guard.New(t.TempDir(), guard.Config{})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := buildMCPCatalog(configuration, nil, gate, &managedCUACatalogBinding{connection: backend, ownerIdentity: "test-owner", mode: desktop.BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(catalog.Close)
	return catalog, gate, backend
}

func TestManagedCUACatalogExplicitAuthority(t *testing.T) {
	t.Parallel()
	catalog, gate, backend := managedCatalogFixture(t, config.MCPConfig{})
	if backend.listCalls.Load() != 0 {
		t.Fatal("construction performed discovery")
	}
	response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Service: managedCUAKey, Limit: 5})
	if err != nil || len(response.Selected) != 2 {
		t.Fatalf("managed search: %+v %v", response, err)
	}
	resolved, err := catalog.Resolve(t.Context(), []string{mcpToolID(managedCUAKey, "click")})
	if err != nil || len(resolved) != 1 {
		t.Fatal("missing managed selection", err)
	}
	name := resolved[0].Tool.Definition().Name
	adapter := &guardAdapter{inner: gate, mcp: catalog}
	decision, err := adapter.Check(t.Context(), llm.ToolCall{Name: name, Arguments: []byte(`{}`)})
	if err != nil || decision.Decision != agent.GuardAllow || decision.Revalidate == nil {
		t.Fatal("managed enablement was not bound", err, decision.Decision)
	}
	gate.ResetSessionGrants()
	decision, err = adapter.Check(t.Context(), llm.ToolCall{Name: name, Arguments: []byte(`{}`)})
	if err != nil || decision.Decision != agent.GuardAllow {
		t.Fatal("explicit Computer Use authorization depended on a Session grant")
	}
	binding, scope, _, err := catalog.MCPBinding(t.Context(), name)
	if err != nil || binding.Source != "managed:computer-use" || binding.ServiceID != "cua" {
		t.Fatal("missing managed provenance", err)
	}
	binding.ToolName = "future_input"
	binding.SchemaFingerprint = mcpDigest([]json.RawMessage{backend.items[2].InputSchema, nil})
	denied, _, err := gate.CheckMCP(t.Context(), "future", binding, scope)
	if err != nil || denied.Decision != guard.DecisionDeny {
		t.Fatal("future tool inherited authority")
	}
}

func TestManagedCUACatalogCannotBeForgedByConfig(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"key", "id", "source"} {
		t.Run(field, func(t *testing.T) {
			configuration := catalogTestConfig("user:ordinary")
			server := configuration.Servers["user:ordinary"]
			switch field {
			case "key":
				server.Key = managedCUAKey
			case "id":
				server.ID = "cua"
			case "source":
				server.Source.Kind = "managed"
			}
			configuration.Servers = map[string]config.MCPServer{server.Key: server}
			if _, err := newMCPCatalog(configuration, nil, ownerTestGuard(t)); err == nil {
				t.Fatal("ordinary config supplied reserved managed identity")
			}
		})
	}
	configuration := catalogTestConfig("user:ordinary")
	server := configuration.Servers["user:ordinary"]
	server.Settings.Name = "Computer Use cua"
	configuration.Servers[server.Key] = server
	backend := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("click", "CUA input")}}
	gate := ownerTestGuard(t)
	catalog, err := newMCPCatalog(configuration, map[string]mcpCatalogConnection{server.Key: backend}, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Limit: 1})
	if err != nil || len(response.Entries) != 1 {
		t.Fatal("ordinary search failed", err)
	}
	decision, err := (&guardAdapter{inner: gate, mcp: catalog}).Check(t.Context(), llm.ToolCall{Name: response.Entries[0].Name, Arguments: []byte(`{}`)})
	if err != nil || decision.Decision != agent.GuardAsk {
		t.Fatal("ordinary CUA name inherited managed authority", err)
	}
}

func TestManagedCUACatalogGrantsRemainServiceBound(t *testing.T) {
	t.Parallel()
	backend := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("click", "Click a window")}}
	gate := ownerTestGuard(t)
	catalog, err := buildMCPCatalog(
		catalogTestConfig("user:ordinary"),
		map[string]mcpCatalogConnection{"user:ordinary": backend},
		gate,
		&managedCUACatalogBinding{connection: backend, ownerIdentity: "test-owner", mode: desktop.BackgroundOnly},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(catalog.Close)
	response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Query: "click", Limit: 5})
	if err != nil || len(response.Entries) != 2 {
		t.Fatal("mixed service discovery", response, err)
	}
	adapter := &guardAdapter{inner: gate, mcp: catalog}
	for _, entry := range response.Entries {
		want := agent.GuardAsk
		if entry.Service == managedCUAKey {
			want = agent.GuardAllow
		}
		decision, err := adapter.Check(t.Context(), llm.ToolCall{Name: entry.Name, Arguments: []byte(`{}`)})
		if err != nil || decision.Decision != want {
			t.Fatalf("service %s: decision=%s want=%s err=%v", entry.Service, decision.Decision, want, err)
		}
	}
}

func TestManagedCUACatalogRestrictionsAndStalePolicy(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"restriction", "service-restriction", "revoke", "remove", "mode", "owner", "deny", "notification", "close"} {
		t.Run(change, func(t *testing.T) {
			catalog, gate, backend := managedCatalogFixture(t, config.MCPConfig{})
			response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{mcpToolID(managedCUAKey, "click")}, Limit: 1})
			if err != nil || len(response.Selected) != 1 {
				t.Fatal("initial search", err)
			}
			selected, err := catalog.Resolve(t.Context(), []string{response.Selected[0].ID})
			if err != nil || len(selected) != 1 {
				t.Fatal("initial resolve", err)
			}
			binding, scope, _, err := catalog.MCPBinding(t.Context(), selected[0].Tool.Definition().Name)
			if err != nil {
				t.Fatal(err)
			}
			decision, permit, err := gate.CheckMCP(t.Context(), selected[0].Tool.Definition().Name, binding, scope)
			if err != nil || decision.Decision != guard.DecisionAllow {
				t.Fatal("initial permit", err)
			}
			policy := guard.MCPService{Source: binding.Source, ServiceID: binding.ServiceID, ConnectionFingerprint: binding.ConnectionFingerprint, PermissionScope: scope, Enabled: true}
			switch change {
			case "restriction":
				policy.Tools = []guard.MCPToolPolicy{{Name: "click", SchemaFingerprint: binding.SchemaFingerprint, Allowed: false}}
			case "service-restriction":
				policy.Enabled = false
			case "revoke":
				gate.RevokeMCPService(binding.Source, binding.ServiceID)
			case "remove":
				gate.RemoveMCPService(binding.Source, binding.ServiceID)
			case "mode":
				policy.PermissionScope += ":changed-mode"
			case "owner":
				policy.ConnectionFingerprint = "replacement-owner"
			case "deny":
				policy.Tools = []guard.MCPToolPolicy{{Name: "click", SchemaFingerprint: binding.SchemaFingerprint, Allowed: true, UserDecision: guard.DecisionDeny}}
			case "notification":
				backend.generation.Add(1)
			case "close":
				catalog.Close()
			}
			if change != "revoke" && change != "remove" && change != "notification" && change != "close" {
				if err := gate.SetMCPService(policy); err != nil {
					t.Fatal(err)
				}
			}
			if change != "notification" && change != "close" && permit.Validate(t.Context()) == nil {
				t.Fatal("old permission remained valid")
			}
			if catalog.Check(t.Context(), response.Selected[0]) == nil {
				t.Fatal("old catalog remained executable")
			}
			result, err := selected[0].Tool.Execute(t.Context(), llm.ToolCall{Name: selected[0].Tool.Definition().Name, Arguments: []byte(`{}`)})
			if err != nil || result.Details.State != llm.ExecutionNotDispatched || backend.calls.Load() != 0 {
				t.Fatal("stale version dispatched", err)
			}
			if change != "notification" && change != "close" {
				next, err := catalog.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{response.Selected[0].ID}, Limit: 1})
				if err != nil || len(next.Selected) != 0 {
					t.Fatal("discovery restored stale authority", err)
				}
			}
		})
	}
	for _, tools := range [][]string{nil, {"click"}} {
		catalog, _, backend := managedCatalogFixture(t, config.MCPConfig{Restrictions: []config.MCPRestriction{{Source: "*", Server: "cua", Tools: tools}}})
		response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{mcpToolID(managedCUAKey, "click")}, Limit: 1})
		if err != nil || len(response.Selected) != 0 {
			t.Fatal("configured restriction ignored", err)
		}
		if tools == nil && backend.listCalls.Load() != 0 {
			t.Fatal("service deny performed discovery")
		}
	}
}

func TestManagedCUACatalogFinalDispatchRevocation(t *testing.T) {
	t.Parallel()
	catalog, gate, backend := managedCatalogFixture(t, config.MCPConfig{})
	catalog.connections[managedCUAKey] = &mcpDispatchFixture{mcpCatalogConnection: backend, before: func() { gate.RevokeMCPService("managed:computer-use", "cua") }}
	response, err := catalog.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{mcpToolID(managedCUAKey, "click")}, Limit: 1})
	if err != nil || len(response.Selected) != 1 {
		t.Fatal("search", err)
	}
	selected, err := catalog.Resolve(t.Context(), []string{response.Selected[0].ID})
	if err != nil || len(selected) != 1 {
		t.Fatal("resolve", err)
	}
	result, err := selected[0].Tool.Execute(context.Background(), llm.ToolCall{Name: selected[0].Tool.Definition().Name, Arguments: []byte(`{}`)})
	if err != nil || result.Details.State != llm.ExecutionNotDispatched || backend.calls.Load() != 0 {
		t.Fatal("final dispatch skipped revocation", err)
	}
}

func TestManagedCUACatalogLoopUsesGenericResultAndHistory(t *testing.T) {
	t.Parallel()
	backend := &managedResultFixture{mcpCatalogFixture: &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("list_windows", "read windows")}}}
	gate := ownerTestGuard(t)
	catalog, err := buildMCPCatalog(config.MCPConfig{}, nil, gate, &managedCUACatalogBinding{connection: backend, ownerIdentity: "loop-owner", mode: desktop.BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	search, err := tool.NewToolSearch(catalog)
	if err != nil {
		t.Fatal(err)
	}
	model := &mcpSearchLoopModel{t: t}
	loop, err := agent.NewLoop(model, []agent.Tool{search}, agent.WithGuard(&guardAdapter{inner: gate, mcp: catalog}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "managed.jsonl")
	store, err := session.Create(t.Context(), path, session.Metadata{ID: "managed", CreatedAt: 1, WorkingDirectory: t.TempDir()})
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
		entry, err := session.NewMessage(fmt.Sprintf("entry-%d", counter), parent, int64(counter), message)
		if err != nil {
			return err
		}
		return store.AppendMessage(ctx, entry)
	}
	prompt, _ := llm.NewUserMessage(llm.NewTextContent("read windows using managed discovery").Part())
	result, err := loop.Run(t.Context(), agent.RunInput{Model: deepseek.DefaultModel(), Prompt: prompt, Catalog: catalog, MessageRecorder: record}, nil)
	if err != nil || backend.calls.Load() != 1 {
		t.Fatal("managed operation failed without extra approval", err, backend.calls.Load())
	}
	var outcome llm.ToolResultMessage
	for _, message := range result.Messages() {
		if value, ok := message.(llm.ToolResultMessage); ok && value.ToolCallID == "execute" {
			outcome = value
		}
	}
	if outcome.Details == nil || outcome.Details.Binding == nil || outcome.Details.Binding.Source != "managed:computer-use" || outcome.Details.Binding.ServiceID != "cua" || outcome.Details.Binding.ToolName != "list_windows" || outcome.Details.State != llm.ExecutionReturned || string(outcome.Details.StructuredContent) != `{"window_id":9007199254740993}` || len(outcome.Content) != 2 || outcome.Content[0].Text != "first" || outcome.Content[1].Text != "last" {
		t.Fatalf("managed source result lost: %+v", outcome)
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
				t.Fatal("managed source changed on Session replay")
			}
		}
	}
	if !found {
		t.Fatal("managed result missing after reopen")
	}
}

type managedResultFixture struct{ *mcpCatalogFixture }

func (f *managedResultFixture) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	f.calls.Add(1)
	return mcpclient.Result{State: llm.ExecutionReturned, StructuredContent: []byte(`{"window_id":9007199254740993}`), Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "first"}, {Kind: mcpclient.BlockText, Text: "last"}}}, nil
}

func TestManagedCUACatalogFreezesNativeModeWithoutIO(t *testing.T) {
	t.Parallel()
	state := managedLifecycleState(t)
	for _, mode := range []config.DesktopControlMode{config.DesktopBackgroundOnly, config.DesktopForegroundAllowed} {
		configuration := config.Config{DesktopEnabled: true, DesktopControlMode: mode, MCP: catalogTestConfig("user:ordinary")}
		ctx, catalog := managedLifecycleCatalog(t, state, configuration, ownerTestGuard(t))
		run, err := state.bound(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(catalog.scopes[managedCUAKey], ":managed-cua:"+string(mode)+":") || string(run.ControlMode()) != string(mode) {
			t.Fatal("catalog did not bind actual native mode")
		}
		if len(configuration.MCP.Servers) != 1 {
			t.Fatal("catalog mutated owner configuration")
		}
	}
}

func TestManagedCUAServerInfoSurvivesRunInterfaceWrapping(t *testing.T) {
	t.Parallel()
	backend := &appDesktopBackend{}
	// Production observers embed this consumer interface. Optional methods on
	// the concrete native Run must not be lost at the application boundary.
	wrapped := struct{ managedDesktopRun }{backend}
	catalog, err := buildMCPCatalog(config.MCPConfig{}, nil, ownerTestGuard(t), &managedCUACatalogBinding{connection: wrapped, ownerIdentity: "info-owner", mode: desktop.BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	reader, err := tool.NewMCPInfo(catalog)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"managed:cua"}`)})
	if err != nil || result.IsError || len(result.Content) != 2 {
		t.Fatal("managed server info unavailable", result, err)
	}
	var view tool.MCPInfoView
	if err := json.Unmarshal([]byte(result.Content[0].Text), &view); err != nil || view.Name != "cua-driver" || view.Version != desktop.DriverVersion || view.Service != managedCUAKey || backend.calls != 0 {
		t.Fatal("server info lost identity or dispatched a native action", view, err)
	}
	catalog.Close()
	result, err = reader.Execute(t.Context(), llm.ToolCall{Name: "mcp_server_info", Arguments: []byte(`{"service":"managed:cua"}`)})
	if err != nil || !result.IsError {
		t.Fatal("closed catalog returned server information", result, err)
	}
}
