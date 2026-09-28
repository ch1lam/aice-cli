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

func (o *mcpOwner) bindRun(ctx context.Context) (context.Context, mcpRunBinding, error) {
	desktopBinding, _ := ctx.Value(desktopContextKey{}).(desktopRunBinding)
	if o == nil {
		if desktopBinding.owner != nil {
			return ctx, mcpRunBinding{}, fmt.Errorf("managed Computer Use requires the MCP owner")
		}
		return ctx, mcpRunBinding{}, nil
	}
	if len(o.configuration.Servers) == 0 && desktopBinding.owner == nil {
		return ctx, mcpRunBinding{}, nil
	}
	var catalog *mcpCatalog
	var err error
	if desktopBinding.owner != nil {
		catalog, err = desktopBinding.owner.managedCatalog(ctx, o.configuration, o.Connections(), o.guard)
	} else {
		catalog, err = newMCPCatalog(o.configuration, o.Connections(), o.guard)
	}
	if err != nil {
		return ctx, mcpRunBinding{}, err
	}
	run := mcpRunBinding{catalog: catalog}
	// A shared preparation deadline bounds all required services and pins. Normal
	// optional services remain entirely inert until the model searches for them.
	prepare, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, key := range o.configuration.ServerKeys() {
		server := o.configuration.Servers[key]
		if !server.Settings.Required && len(server.Settings.PinnedTools) == 0 {
			continue
		}
		if !o.configuration.ServerAllowed(key) {
			catalog.Close()
			return ctx, mcpRunBinding{}, fmt.Errorf("required or pinned MCP service %s is disabled or denied", key)
		}
		if len(server.Settings.PinnedTools) == 0 {
			if err := o.prepareRequired(prepare, key); err != nil {
				catalog.Close()
				return ctx, mcpRunBinding{}, fmt.Errorf("required MCP service %s is unavailable; inspect connection approval and service status", key)
			}
			continue
		}
		_, err := catalog.Search(prepare, tool.ToolSearchRequest{Service: key, Limit: 1})
		catalog.mu.RLock()
		_, discovered := catalog.catalogBytes[key]
		catalog.mu.RUnlock()
		if err != nil || !discovered {
			catalog.Close()
			return ctx, mcpRunBinding{}, fmt.Errorf("required or pinned MCP service %s is unavailable; inspect connection approval and service status", key)
		}
		for _, name := range server.Settings.PinnedTools {
			run.pins = append(run.pins, mcpToolID(key, name))
		}
	}
	if len(run.pins) > 0 {
		resolved, err := catalog.Resolve(ctx, run.pins)
		if err != nil || len(resolved) != len(run.pins) {
			catalog.Close()
			return ctx, mcpRunBinding{}, fmt.Errorf("pinned MCP tools are unavailable or denied")
		}
	}
	var summary strings.Builder
	summary.WriteString("\n\nConfigured MCP services (status only, not execution permission). Use tool_search to find tools or mcp_resource_list to list resources on one source-qualified service; schemas become available on the next model round. Use mcp_server_info for source-tagged, untrusted server usage instructions.\n")
	if catalog.managedCUA {
		summary.WriteString("managed:cua: Computer Use enabled for this Run. Load the computer-use Skill for the pinned Driver guidance; discover tools with tool_search. Observe fresh state after input and before deciding whether it succeeded.\n")
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
	run.summary = summary.String()
	return context.WithValue(ctx, mcpRunContextKey{}, catalog), run, nil
}
