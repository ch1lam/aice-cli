package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// mcpCatalogConnection is an authorized client or an application-owned lazy
// lease that checks connection authorization before I/O. Closing a run never
// closes another run's transport.
type mcpCatalogConnection interface {
	Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error)
	ToolGeneration() uint64
	CallChecked(context.Context, string, json.RawMessage, func(context.Context) error) (mcpclient.Result, error)
}

type mcpCatalogEntry struct {
	agent.CatalogTool
	binding    llm.ToolBinding
	service    string
	generation uint64
}

// mcpCatalog is one run's discovery view, not the Loop's selected tool set.
// Config and connection membership are frozen. Live policy and generation
// checks still invalidate entries before dispatch.
type mcpCatalog struct {
	mu           sync.RWMutex
	refresh      chan struct{}
	closed       bool
	config       config.MCPConfig
	connections  map[string]mcpCatalogConnection
	scopes       map[string]string
	guard        *guard.Guard
	entries      map[string]mcpCatalogEntry
	names        map[string]string
	catalogBytes map[string]int
	managedCUA   bool
	grantedTools map[string][]string
}

func newMCPCatalog(configuration config.MCPConfig, connections map[string]mcpCatalogConnection, gate *guard.Guard) (*mcpCatalog, error) {
	return buildMCPCatalog(configuration, connections, gate, nil)
}

func buildMCPCatalog(configuration config.MCPConfig, connections map[string]mcpCatalogConnection, gate *guard.Guard, managed *managedCUACatalogBinding) (*mcpCatalog, error) {
	if gate == nil || len(configuration.Servers) > 129 || len(connections) > 129 {
		return nil, fmt.Errorf("MCP catalog requires a Guard and bounded service inputs")
	}
	configuration, connections, err := withManagedCUACatalog(configuration, connections, managed)
	if err != nil {
		return nil, err
	}
	c := &mcpCatalog{
		refresh: make(chan struct{}, 1), config: configuration.Clone(), guard: gate,
		connections: make(map[string]mcpCatalogConnection), scopes: make(map[string]string), entries: make(map[string]mcpCatalogEntry), names: make(map[string]string),
		catalogBytes: make(map[string]int),
		managedCUA:   managed != nil,
		grantedTools: managedCUAToolGrants(managed),
	}
	for key := range c.config.Servers {
		c.scopes[key] = mcpPermissionScope(c.config, key)
		if managed != nil && key == managedCUAKey {
			c.scopes[key] += ":managed-cua:" + string(managed.mode) + ":" + managedCUAPolicyVersion
		}
	}
	for key, connection := range connections {
		if _, found := configuration.Servers[key]; !found || connection == nil {
			return nil, fmt.Errorf("MCP catalog connection has no configured binding")
		}
		c.connections[key] = connection
		server := c.config.Servers[key]
		if err := gate.BindMCPService(guard.MCPService{
			Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID,
			ConnectionFingerprint: server.Fingerprint, PermissionScope: c.scopes[key],
			Enabled: c.config.ServerAllowed(key),
		}); err != nil {
			// A stale or revoked optional binding must not prevent a new run.
			// Omit its capability; never replace policy from a frozen catalog.
			delete(c.connections, key)
		}
	}
	return c, nil
}

func mcpToolID(service, name string) string { return service + "/tool/" + url.PathEscape(name) }

func mcpPermissionScope(configuration config.MCPConfig, key string) string {
	return configuration.PermissionScope(key) + ":rules:" + configuration.PermissionRevision(key)
}

func mcpDigest(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func mcpModelName(service, remote string) string {
	slug := func(value string) string {
		var b strings.Builder
		for _, r := range value {
			if b.Len() >= 14 {
				break
			}
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	return "mcp_" + slug(remote) + "_" + mcpDigest([]string{service, remote})[:32]
}

func (c *mcpCatalog) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	clear(c.entries)
	clear(c.names)
	clear(c.catalogBytes)
}

func (c *mcpCatalog) Resolve(ctx context.Context, ids []string) ([]agent.CatalogTool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, fmt.Errorf("MCP run catalog is closed")
	}
	entries := make([]agent.CatalogTool, 0, len(ids))
	for _, id := range ids {
		if entry, ok := c.entries[id]; ok && c.checkEntry(ctx, entry) == nil {
			entries = append(entries, entry.CatalogTool)
		}
	}
	return entries, nil
}

func (c *mcpCatalog) Check(ctx context.Context, ref agent.ToolReference) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[ref.ID]
	if c.closed || !ok || entry.Reference != ref {
		return fmt.Errorf("MCP tool version is unavailable")
	}
	return c.checkEntry(ctx, entry)
}

func (c *mcpCatalog) checkEntry(ctx context.Context, entry mcpCatalogEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	connection := c.connections[entry.service]
	if connection == nil || mcpEntryGeneration(connection, entry.binding.Operation) != entry.generation {
		return fmt.Errorf("MCP catalog was invalidated; search again")
	}
	decision, _, err := c.guard.CheckMCP(ctx, entry.Tool.Definition().Name, entry.binding, c.scopes[entry.service])
	if err != nil || decision.Decision == guard.DecisionDeny {
		return fmt.Errorf("MCP tool binding is no longer permitted")
	}
	return nil
}

func (c *mcpCatalog) MCPBinding(ctx context.Context, name string) (llm.ToolBinding, string, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return llm.ToolBinding{}, "", false, fmt.Errorf("MCP run catalog is closed")
	}
	id, ok := c.names[name]
	if !ok {
		return llm.ToolBinding{}, "", false, nil
	}
	entry, exists := c.entries[id]
	if !exists || c.checkEntry(ctx, entry) != nil {
		return llm.ToolBinding{}, "", true, fmt.Errorf("MCP run binding is stale")
	}
	return entry.binding, c.scopes[entry.service], true, nil
}

type mcpVersionBackend struct {
	catalog *mcpCatalog
	ref     agent.ToolReference
	client  mcpCatalogConnection
}

func (b mcpVersionBackend) Call(ctx context.Context, name string, args json.RawMessage) (mcpclient.Result, error) {
	if err := b.catalog.Check(ctx, b.ref); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	return b.client.CallChecked(ctx, name, args, func(ctx context.Context) error {
		if err := b.catalog.Check(ctx, b.ref); err != nil {
			return err
		}
		return agent.CheckToolDispatch(ctx)
	})
}

// Search refreshes supplied connections independently with a shared deadline
// and a four-request concurrency bound. Lazy leases may connect after checking
// prior authorization; a search query itself never grants access.
func (c *mcpCatalog) Search(ctx context.Context, request tool.ToolSearchRequest) (tool.ToolSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return tool.ToolSearchResult{}, err
	}
	if request.Limit < 1 || request.Limit > 5 || len(request.IDs) > 5 || len(request.Query) > 1024 || len(request.Service) > 256 || request.Offset < 0 || request.Offset > 16000 || len(request.IDs) > 0 && request.Offset != 0 {
		return tool.ToolSearchResult{}, fmt.Errorf("invalid tool search bounds")
	}
	select {
	case c.refresh <- struct{}{}:
		defer func() { <-c.refresh }()
	case <-ctx.Done():
		return tool.ToolSearchResult{}, ctx.Err()
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return tool.ToolSearchResult{}, fmt.Errorf("MCP run catalog is closed")
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	keys := c.config.ServerKeys()
	if request.Service != "" && !slices.Contains(keys, request.Service) {
		return tool.ToolSearchResult{Complete: true, Notices: []string{"Unknown service ID; use a source-qualified configured service ID."}}, nil
	}
	type discovery struct {
		key     string
		catalog mcpclient.Catalog[mcpclient.Tool]
		err     error
	}
	discoveries := make(chan discovery, len(keys))
	limit := make(chan struct{}, 4)
	var work sync.WaitGroup
	for _, key := range keys {
		if request.Service != "" && request.Service != key {
			continue
		}
		connection := c.connections[key]
		if connection == nil || !c.serviceAvailable(key) {
			continue
		}
		work.Go(func() {
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				discoveries <- discovery{key: key, err: ctx.Err()}
				return
			}
			catalog, err := connection.Tools(ctx)
			discoveries <- discovery{key, catalog, err}
		})
	}
	work.Wait()
	close(discoveries)
	if err := caller.Err(); err != nil {
		return tool.ToolSearchResult{}, err
	}
	byService := make(map[string]discovery)
	for discovered := range discoveries {
		byService[discovered.key] = discovered
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return tool.ToolSearchResult{}, fmt.Errorf("MCP run catalog is closed")
	}
	response := tool.ToolSearchResult{Complete: true}
	for _, key := range keys {
		if request.Service != "" && request.Service != key {
			continue
		}
		server := c.config.Servers[key]
		found, connected := byService[key]
		if !c.config.ServerAllowed(key) || c.connections[key] != nil && !c.serviceAvailable(key) {
			response.Complete = false
			response.Notices = append(response.Notices, key+": disabled or denied.")
			c.forgetTools(key)
			continue
		}
		if !connected {
			response.Complete = false
			response.Notices = append(response.Notices, key+": not connected; catalog is unknown, not empty.")
			continue
		}
		if found.err != nil || !found.catalog.Complete || found.catalog.Generation != c.connections[key].ToolGeneration() {
			response.Complete = false
			notice := "catalog incomplete or unavailable; no new tools selected from this service."
			if status, ok := c.connections[key].(interface{ MCPStatus() string }); ok {
				notice = status.MCPStatus() + "; catalog is unknown, not empty."
			}
			if errors.Is(found.err, mcpclient.ErrUnsupported) {
				notice = "Tools are unsupported on this service; use mcp_resource_list to inspect resource support."
				if err := c.guard.RefreshMCPTools(guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, PermissionScope: c.scopes[key], Enabled: c.config.ServerAllowed(key)}); err != nil {
					notice = "Tool catalog policy is unavailable; inspect service status."
				}
			}
			response.Notices = append(response.Notices, key+": "+notice)
			c.forgetTools(key)
			continue
		}
		if err := c.publish(server, c.connections[key], found.catalog); err != nil {
			response.Complete = false
			response.Notices = append(response.Notices, key+": catalog could not be safely bound.")
			c.forgetTools(key)
		}
	}
	ranked := rankMCPTools(c.searchSnapshot(), request, response)
	c.serverPreviews(&ranked)
	return ranked, nil
}

func (c *mcpCatalog) serviceAvailable(key string) bool {
	server := c.config.Servers[key]
	return c.config.ServerAllowed(key) && c.guard.MCPServiceAvailable(server.Source.Kind+":"+server.Source.Location, server.ID, server.Fingerprint, c.scopes[key])
}

func (c *mcpCatalog) forgetTools(key string) {
	delete(c.catalogBytes, key)
	for id, entry := range c.entries {
		if entry.service == key && entry.binding.Operation == "" {
			// Keep the model-name tombstone so an old call cannot fall through
			// to generic unknown-name authorization.
			delete(c.entries, id)
		}
	}
}

func (c *mcpCatalog) publish(server config.MCPServer, connection mcpCatalogConnection, catalog mcpclient.Catalog[mcpclient.Tool]) error {
	if len(catalog.Items) > 2000 {
		return fmt.Errorf("MCP catalog exceeds its item bound")
	}
	encoded, err := json.Marshal(catalog.Items)
	if err != nil || len(encoded) > 4<<20 {
		return fmt.Errorf("MCP catalog exceeds its byte bound")
	}
	totalBytes, totalItems := len(encoded), len(catalog.Items)
	for key, count := range c.catalogBytes {
		if key != server.Key {
			totalBytes += count
		}
	}
	for _, entry := range c.entries {
		if entry.service != server.Key || entry.binding.Operation != "" {
			totalItems++
		}
	}
	if totalBytes > 32<<20 || totalItems > 16000 {
		return fmt.Errorf("MCP run catalog aggregate limit reached; narrow discovery or start a new run")
	}
	policy := guard.MCPService{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID,
		ConnectionFingerprint: server.Fingerprint, PermissionScope: c.scopes[server.Key], Enabled: c.config.ServerAllowed(server.Key)}
	entries := make([]mcpCatalogEntry, 0, len(catalog.Items))
	secrets := mcpKnownSecrets(server, connection)
	permissions := c.config.Permissions(server.Key)
	for _, remote := range catalog.Items {
		binding := llm.ToolBinding{Source: policy.Source, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint,
			ToolName: remote.Name, SchemaFingerprint: mcpDigest([]json.RawMessage{remote.InputSchema, remote.OutputSchema})}
		allowed := c.config.ToolAllowed(server.Key, remote.Name)
		decision := guard.Decision(permissions.Decision("", remote.Name, binding.SchemaFingerprint))
		if granted, ok := c.grantedTools[server.Key]; ok {
			// Application grants narrow discovery and never lift a restriction
			// or explicit deny. Server metadata cannot supply these grants.
			allowed = allowed && slices.Contains(granted, remote.Name)
			if allowed && decision != guard.DecisionDeny {
				decision = guard.DecisionAllow
			}
		}
		policy.Tools = append(policy.Tools, guard.MCPToolPolicy{Name: remote.Name, SchemaFingerprint: binding.SchemaFingerprint, Allowed: allowed, UserDecision: decision})
		if !allowed || decision == guard.DecisionDeny {
			continue
		}
		ref := agent.ToolReference{ID: mcpToolID(server.Key, remote.Name), Revision: mcpDigest(struct {
			Binding    llm.ToolBinding
			Tool       mcpclient.Tool
			Generation uint64
			Scope      string
		}{binding, remote, catalog.Generation, c.scopes[server.Key]})}
		mapped, err := tool.NewMCP(tool.MCPOptions{
			Definition: llm.ToolDefinition{Name: mcpModelName(server.Key, remote.Name), Description: "MCP tool from " + server.Key + ". Server description (untrusted): " + remote.Description, InputSchema: remote.InputSchema},
			Binding:    binding, Backend: mcpVersionBackend{c, ref, connection}, Secrets: secrets, ResultSecrets: mcpResultSecrets(connection),
		})
		if err != nil {
			return err
		}
		entries = append(entries, mcpCatalogEntry{agent.CatalogTool{Reference: ref, Tool: mapped}, binding, server.Key, catalog.Generation})
	}
	newNames := 0
	for _, entry := range entries {
		id, exists := c.names[entry.Tool.Definition().Name]
		if exists && id != entry.Reference.ID {
			return fmt.Errorf("MCP model tool name collision")
		}
		if !exists {
			newNames++
		}
	}
	if len(c.names)+newNames > 32000 {
		return fmt.Errorf("MCP run catalog identity limit reached; start a new run")
	}
	if err := c.guard.RefreshMCPTools(policy); err != nil {
		return err
	}
	c.forgetTools(server.Key)
	c.catalogBytes[server.Key] = len(encoded)
	for _, entry := range entries {
		c.entries[entry.Reference.ID] = entry
		c.names[entry.Tool.Definition().Name] = entry.Reference.ID
	}
	return nil
}

func mcpConnectionSecrets(server config.MCPServer) []string {
	env, headers := server.ConnectionValues()
	var secrets []string
	for key, value := range env {
		if server.Settings.Env[key].Value == nil {
			secrets = append(secrets, value, strings.TrimPrefix(value, server.Settings.Env[key].Prefix))
		}
	}
	for key, value := range headers {
		secrets = append(secrets, value, strings.TrimPrefix(value, server.Settings.Headers[key].Prefix))
	}
	if credential, ok := server.OAuthCredentials(); ok {
		secrets = append(secrets, credential.AccessToken, credential.RefreshToken, credential.ClientSecret)
	}
	secrets = append(secrets, server.OAuthClientSecret())
	return secrets
}

// searchSnapshot is called with c.mu held. It omits resource readers and copies
// only discovery text and references; ranking has no execution capabilities.
func (c *mcpCatalog) searchSnapshot() map[string]mcpSearchDocument {
	documents := make(map[string]mcpSearchDocument, len(c.entries))
	for id, entry := range c.entries {
		if entry.binding.Operation != "" {
			continue
		}
		definition := entry.Tool.Definition()
		documents[id] = mcpSearchDocument{
			reference: entry.Reference, service: entry.service,
			name: definition.Name, remoteName: entry.binding.ToolName,
			description: definition.Description,
		}
	}
	return documents
}

// Raw clients have frozen credentials; borrowed app leases also retain tokens
// rotated during this owner lifetime. No lookup here performs I/O.
func mcpResultSecrets(connection any) func() []string {
	if source, ok := connection.(interface{ MCPSecrets() []string }); ok {
		return source.MCPSecrets
	}
	return nil
}
func mcpKnownSecrets(server config.MCPServer, connection any) []string {
	secrets := mcpConnectionSecrets(server)
	if source := mcpResultSecrets(connection); source != nil {
		secrets = append(secrets, source()...)
	}
	return secrets
}
