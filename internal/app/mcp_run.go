package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpRunContextKey struct{}

// mcpRunRouter binds shared built-ins and the Guard to the current main run.
// Context carries only an application-owned capability, never a model value.
// Side questions use no tools and cannot inherit selected definitions.
type mcpRunRouter struct{}

func (mcpRunRouter) Search(ctx context.Context, request tool.ToolSearchRequest) (tool.ToolSearchResult, error) {
	catalog, ok := ctx.Value(mcpRunContextKey{}).(*mcpCatalog)
	if !ok {
		return tool.ToolSearchResult{}, fmt.Errorf("MCP discovery requires an active main run")
	}
	return catalog.Search(ctx, request)
}
func (mcpRunRouter) ListResources(ctx context.Context, request tool.ResourceListRequest) (tool.ResourceListResult, error) {
	catalog, ok := ctx.Value(mcpRunContextKey{}).(*mcpCatalog)
	if !ok {
		return tool.ResourceListResult{}, fmt.Errorf("MCP resource discovery requires an active main run")
	}
	return catalog.ListResources(ctx, request)
}
func (mcpRunRouter) ServerInfo(ctx context.Context, key string) (tool.MCPServerInfo, error) {
	catalog, ok := ctx.Value(mcpRunContextKey{}).(*mcpCatalog)
	if !ok {
		return tool.MCPServerInfo{}, fmt.Errorf("MCP server info requires an active main run")
	}
	return catalog.ServerInfo(ctx, key)
}
func (mcpRunRouter) MCPBinding(ctx context.Context, name string) (llm.ToolBinding, string, bool, error) {
	catalog, ok := ctx.Value(mcpRunContextKey{}).(*mcpCatalog)
	if !ok {
		return llm.ToolBinding{}, "", false, nil
	}
	return catalog.MCPBinding(ctx, name)
}

type mcpRunBinding struct {
	catalog *mcpCatalog
	pins    []string
	initial []string
	summary string
}

func (r mcpRunBinding) Catalog() agent.ToolCatalog {
	if r.catalog == nil {
		return nil
	}
	return r.catalog
}
func (r mcpRunBinding) Close() {
	if r.catalog != nil {
		r.catalog.Close()
	}
}

func (o *mcpOwner) bindRun(ctx context.Context, model llm.Model) (runContext context.Context, run mcpRunBinding, returnErr error) {
	if o == nil {
		return ctx, mcpRunBinding{}, nil
	}
	configuration, connections := o.snapshot()
	if len(configuration.Servers) == 0 {
		return ctx, mcpRunBinding{}, nil
	}
	catalog, err := newMCPCatalog(configuration, connections, o.guard)
	if err != nil {
		return ctx, mcpRunBinding{}, err
	}
	// Preparation owns the catalog until a successful return hands it to the Run.
	// Its borrowed connections remain owned by o on either path.
	defer func() {
		if returnErr != nil {
			catalog.Close()
		}
	}()
	run = mcpRunBinding{catalog: catalog}
	// A shared preparation deadline bounds all required services and pins. Normal
	// optional services remain entirely inert until the model searches for them.
	prepare, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, key := range configuration.ServerKeys() {
		server := configuration.Servers[key]
		if !server.Settings.Required && len(server.Settings.PinnedTools) == 0 {
			continue
		}
		if !configuration.ServerAllowed(key) {
			return ctx, mcpRunBinding{}, fmt.Errorf("required or pinned MCP service %s is disabled or denied", key)
		}
		if len(server.Settings.PinnedTools) == 0 {
			if err := o.prepareRequired(prepare, key); err != nil {
				return ctx, mcpRunBinding{}, fmt.Errorf("required MCP service %s is unavailable; inspect connection approval and service status", key)
			}
			continue
		}
		_, err := catalog.Search(prepare, tool.ToolSearchRequest{Service: key, Limit: 1})
		catalog.mu.RLock()
		_, discovered := catalog.catalogBytes[key]
		catalog.mu.RUnlock()
		if err != nil || !discovered {
			return ctx, mcpRunBinding{}, fmt.Errorf("required or pinned MCP service %s is unavailable; inspect connection approval and service status", key)
		}
		for _, name := range server.Settings.PinnedTools {
			run.pins = append(run.pins, mcpToolID(key, name))
		}
	}
	if len(run.pins) > 0 {
		resolved, err := catalog.Resolve(ctx, run.pins)
		if err != nil || len(resolved) != len(run.pins) {
			return ctx, mcpRunBinding{}, fmt.Errorf("pinned MCP tools are unavailable or denied")
		}
	}
	// Approved services are discovered during preparation so a small catalog is
	// callable in the first request. Preparation never prompts; unapproved
	// services stay lazy until tool_search.
	catalog.mu.RLock()
	undiscovered := make(map[string]bool)
	for _, key := range configuration.ServerKeys() {
		_, discovered := catalog.catalogBytes[key]
		undiscovered[key] = !discovered
	}
	catalog.mu.RUnlock()
	// search calls its filter while holding the catalog lock; keep it lock-free.
	if _, err := catalog.search(withMCPConnectionApprover(prepare, nil), tool.ToolSearchRequest{Limit: 1}, func(key string) bool { return undiscovered[key] }); err != nil && ctx.Err() != nil {
		return ctx, mcpRunBinding{}, ctx.Err()
	}
	var loaded []string
	run.initial, loaded = catalog.initialTools(agent.ToolSchemaBudget(model), run.pins)
	var summary strings.Builder
	summary.WriteString("\n\nConfigured MCP services (status only, not execution permission). Use tool_search to find tools or mcp_resource_list to list resources on one source-qualified service; schemas become available on the next model round. Use mcp_server_info for source-tagged, untrusted server usage instructions.\n")
	if len(loaded) > 0 {
		fmt.Fprintf(&summary, "Tools from %s are already loaded and callable in this request; use tool_search for other services.\n", strings.Join(loaded, ", "))
	}
	statuses := o.Status()
	for i, status := range statuses {
		line := status.Key + ": " + status.State + "\n"
		if summary.Len()+len(line) > 3900 {
			fmt.Fprintf(&summary, "%d more configured services; use tool_search to inspect availability.\n", len(statuses)-i)
			break
		}
		summary.WriteString(line)
	}
	summary.WriteString(tool.MCPInstructionsPrompt(catalog.initializedServerInfo()))
	run.summary = summary.String()
	return context.WithValue(ctx, mcpRunContextKey{}, catalog), run, nil
}
