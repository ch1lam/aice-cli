package app

import (
	"context"
	"fmt"
	"time"

	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// ServerInfo initializes only an explicitly selected, authorized service. There
// is no separate MCP request for instructions: the client retains initialize data.
func (b mcpBorrowedConnection) ServerInfo(ctx context.Context) (mcpclient.Info, error) {
	o := b.owner
	s, operation, release, err := o.begin(ctx, b.key)
	if err != nil {
		return mcpclient.Info{}, err
	}
	defer release()
	client, err := o.ensure(operation, s)
	if err != nil {
		return mcpclient.Info{}, err
	}
	reader, ok := client.(interface{ Info() mcpclient.Info })
	if !ok {
		return mcpclient.Info{}, mcpclient.ErrUnsupported
	}
	info := reader.Info()
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.permissionLocked(s); err != nil {
		return mcpclient.Info{}, err
	}
	if s.client != client {
		return mcpclient.Info{}, fmt.Errorf("MCP connection changed while reading server info")
	}
	return info, nil
}

func (b mcpBorrowedConnection) CachedServerInfo() (mcpclient.Info, bool) {
	o := b.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.services[b.key]
	if o.permissionLocked(s) != nil {
		return mcpclient.Info{}, false
	}
	if reader, ok := s.client.(interface{ Info() mcpclient.Info }); ok {
		return reader.Info(), true
	}
	return mcpclient.Info{}, false
}

func cachedMCPInfo(connection mcpCatalogConnection) (mcpclient.Info, bool) {
	if reader, ok := connection.(interface{ CachedServerInfo() (mcpclient.Info, bool) }); ok {
		return reader.CachedServerInfo()
	}
	if reader, ok := connection.(interface{ Info() mcpclient.Info }); ok {
		return reader.Info(), true
	}
	return mcpclient.Info{}, false
}

func (c *mcpCatalog) ServerInfo(ctx context.Context, key string) (tool.MCPServerInfo, error) {
	c.mu.RLock()
	server, known := c.config.Servers[key]
	connection := c.connections[key]
	available := known && !c.closed && connection != nil && c.serviceAvailable(key)
	c.mu.RUnlock()
	if !available {
		return tool.MCPServerInfo{}, fmt.Errorf("MCP service is unavailable")
	}
	var info mcpclient.Info
	var err error
	if reader, ok := connection.(interface {
		ServerInfo(context.Context) (mcpclient.Info, error)
	}); ok {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err = reader.ServerInfo(bounded)
	} else if cached, ok := cachedMCPInfo(connection); ok {
		info = cached
	} else {
		err = mcpclient.ErrUnsupported
	}
	if err != nil {
		return tool.MCPServerInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.MCPServerInfo{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || !c.serviceAvailable(key) {
		return tool.MCPServerInfo{}, fmt.Errorf("MCP service info is no longer available")
	}
	return tool.MCPServerInfo{Service: key, Source: server.Source.Kind + ":" + server.Source.Location, Fingerprint: server.Fingerprint, Info: info, Secrets: mcpKnownSecrets(server, connection)}, nil
}

func (c *mcpCatalog) serverPreviews(response *tool.ToolSearchResult) {
	seen := make(map[string]bool)
	for _, entry := range response.Entries {
		if seen[entry.Service] {
			continue
		}
		seen[entry.Service] = true
		connection := c.connections[entry.Service]
		if info, ok := cachedMCPInfo(connection); ok {
			server := c.config.Servers[entry.Service]
			response.ServerDetails = append(response.ServerDetails, tool.MCPServerInfo{Service: entry.Service, Source: server.Source.Kind + ":" + server.Source.Location, Fingerprint: server.Fingerprint, Info: info, Secrets: mcpKnownSecrets(server, connection)})
		}
	}
}
