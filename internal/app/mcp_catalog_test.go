package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpCatalogFixture struct {
	generation atomic.Uint64
	calls      atomic.Int32
	listCalls  atomic.Int32
	items      []mcpclient.Tool
	incomplete bool
	list       func(context.Context)
}

func (f *mcpCatalogFixture) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	f.listCalls.Add(1)
	if f.list != nil {
		f.list(ctx)
	}
	return mcpclient.Catalog[mcpclient.Tool]{Items: f.items, Generation: f.generation.Load(), Complete: !f.incomplete}, ctx.Err()
}
func (f *mcpCatalogFixture) ToolGeneration() uint64 { return f.generation.Load() }
func (f *mcpCatalogFixture) Call(context.Context, string, json.RawMessage) (mcpclient.Result, error) {
	f.calls.Add(1)
	return mcpclient.Result{State: llm.ExecutionReturned}, nil
}

func (f *mcpCatalogFixture) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	return f.Call(ctx, name, args)
}

func catalogTestConfig(keys ...string) config.MCPConfig {
	c := config.MCPConfig{Servers: make(map[string]config.MCPServer)}
	for _, key := range keys {
		c.Servers[key] = config.MCPServer{Key: key, ID: "docs", Source: config.Source{Kind: "user", Location: key + ".json"}, Fingerprint: "connection-" + key, Enabled: true}
	}
	return c
}

func catalogFixtureTool(name, description string) mcpclient.Tool {
	return mcpclient.Tool{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func TestMCPCatalogSearchAndExactIdentity(t *testing.T) {
	t.Parallel()
	a := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("read_wiki", "读取公开文档目录"), catalogFixtureTool("search", "Search documents")}}
	b := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("read_wiki", "读取公开文档目录")}}
	g, _ := guard.New("", guard.Config{})
	c, err := newMCPCatalog(catalogTestConfig("user:docs", "project:docs", "user:offline"), map[string]mcpCatalogConnection{"user:docs": a, "project:docs": b}, g)
	if err != nil {
		t.Fatal(err)
	}
	if a.listCalls.Load() != 0 || b.listCalls.Load() != 0 {
		t.Fatal("constructor performed discovery")
	}
	response, err := c.Search(t.Context(), tool.ToolSearchRequest{Query: "文档", Limit: 5})
	if err != nil || len(response.Selected) != 2 || response.Complete {
		t.Fatalf("search=%+v %v", response, err)
	}
	if !strings.Contains(strings.Join(response.Notices, " "), "catalog is unknown") {
		t.Fatal("unconnected catalog reported as empty")
	}
	if response.Entries[0].Name == response.Entries[1].Name || len(response.Entries[0].Name) > 64 {
		t.Fatal("same remote names collided")
	}
	exact := response.Selected[0]
	response, err = c.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{exact.ID}, Limit: 1})
	if err != nil || len(response.Selected) != 1 || response.Selected[0] != exact {
		t.Fatalf("exact selection=%+v %v", response, err)
	}
	resolved, err := c.Resolve(t.Context(), []string{exact.ID})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolve=%v %v", resolved, err)
	}
	definition := resolved[0].Tool.Definition()
	definition.InputSchema[0] = '!'
	if !json.Valid(resolved[0].Tool.Definition().InputSchema) {
		t.Fatal("mutable schema escaped")
	}
	if err := c.Check(t.Context(), exact); err != nil {
		t.Fatal(err)
	}
	a.generation.Add(1)
	b.generation.Add(1)
	if c.Check(t.Context(), exact) == nil {
		t.Fatal("notification failed to invalidate old version")
	}
	result, _ := resolved[0].Tool.Execute(t.Context(), llm.ToolCall{Name: resolved[0].Tool.Definition().Name, Arguments: []byte(`{}`)})
	if result.Details.State != llm.ExecutionNotDispatched || a.calls.Load()+b.calls.Load() != 0 {
		t.Fatal("invalidated adapter dispatched")
	}
	response, err = c.Search(t.Context(), tool.ToolSearchRequest{IDs: []string{exact.ID}, Limit: 1})
	if err != nil || len(response.Selected) != 1 || response.Selected[0].Revision == exact.Revision {
		t.Fatal("refresh did not create a new immutable version")
	}
	c.Close()
	before := a.listCalls.Load() + b.listCalls.Load()
	if _, err := c.Search(t.Context(), tool.ToolSearchRequest{Limit: 5}); err == nil {
		t.Fatal("closed catalog searched")
	}
	if before != a.listCalls.Load()+b.listCalls.Load() {
		t.Fatal("closed catalog made network requests")
	}
}

func TestMCPCatalogRefreshCannotUndoPolicyChanges(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"disable", "revoke", "remove", "connection", "scope", "filter", "direct-deny"} {
		t.Run(change, func(t *testing.T) {
			configuration := catalogTestConfig("user:docs")
			client := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("read", "read")}}
			g, _ := guard.New("", guard.Config{})
			c, _ := newMCPCatalog(configuration, map[string]mcpCatalogConnection{"user:docs": client}, g)
			first, err := c.Search(t.Context(), tool.ToolSearchRequest{Limit: 5})
			if err != nil || len(first.Selected) != 1 {
				t.Fatalf("first=%+v %v", first, err)
			}
			server := configuration.Servers["user:docs"]
			policy := guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, PermissionScope: c.scopes[server.Key], Enabled: true}
			switch change {
			case "disable":
				policy.Enabled = false
			case "connection":
				policy.ConnectionFingerprint = "new-account"
			case "scope":
				policy.PermissionScope = "new-scope"
			case "filter":
				server.Settings.ExcludeTools = []string{"read"}
				configuration.Servers[server.Key] = server
				policy.PermissionScope = mcpPermissionScope(configuration, server.Key)
			case "direct-deny":
				policy.Tools = []guard.MCPToolPolicy{{Name: "read", SchemaFingerprint: "current", Allowed: false}}
			case "revoke":
				g.RevokeMCPService(policy.Source, policy.ServiceID)
			case "remove":
				g.RemoveMCPService(policy.Source, policy.ServiceID)
			}
			if change != "remove" && change != "revoke" {
				if err := g.SetMCPService(policy); err != nil {
					t.Fatal(err)
				}
			}
			next, err := c.Search(t.Context(), tool.ToolSearchRequest{Limit: 5})
			if err != nil || next.Complete || len(next.Selected) != 0 || c.Check(t.Context(), first.Selected[0]) == nil {
				t.Fatalf("stale config restored policy: %+v %v", next, err)
			}
		})
	}
}

func TestMCPCatalogIndependentDiscoveryAndLimits(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	fastDone := make(chan struct{})
	fast := &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("read", "Read")}, list: func(context.Context) { <-started; close(fastDone) }}
	slow := &mcpCatalogFixture{incomplete: true, list: func(ctx context.Context) {
		close(started)
		select {
		case <-fastDone:
		case <-ctx.Done():
		}
	}}
	g, _ := guard.New("", guard.Config{})
	c, _ := newMCPCatalog(catalogTestConfig("a", "b"), map[string]mcpCatalogConnection{"a": slow, "b": fast}, g)
	response, err := c.Search(t.Context(), tool.ToolSearchRequest{Limit: 5})
	if err != nil || response.Complete || len(response.Selected) != 1 || response.Entries[0].Service != "b" {
		t.Fatalf("slow service blocked useful discovery: %+v %v", response, err)
	}
	configuration := catalogTestConfig("many")
	many := &mcpCatalogFixture{}
	for i := range 9 {
		many.items = append(many.items, catalogFixtureTool(fmt.Sprint(i), "tool"))
	}
	c, _ = newMCPCatalog(configuration, map[string]mcpCatalogConnection{"many": many}, g)
	response, err = c.Search(t.Context(), tool.ToolSearchRequest{Limit: 5})
	if err != nil || response.Complete || len(response.Selected) != 5 {
		t.Fatalf("unbounded search: %+v %v", response, err)
	}
	if response.NextOffset == nil {
		t.Fatal("browse cannot continue")
	}
	next, err := c.Search(t.Context(), tool.ToolSearchRequest{Service: "many", Limit: 5, Offset: *response.NextOffset})
	if err != nil || !next.Complete || len(next.Selected) != 4 || next.NextOffset != nil {
		t.Fatalf("second browse page: %+v %v", next, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Search(ctx, tool.ToolSearchRequest{Limit: 5}); err == nil {
		t.Fatal("canceled search succeeded")
	}
}
