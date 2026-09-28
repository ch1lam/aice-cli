package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func storedMCPPermissions(c config.MCPConfig, key string) []interaction.MCPPermission {
	var result []interaction.MCPPermission
	fingerprint, scope, rules := c.StoredPermissions(key)
	applicable := fingerprint == c.Servers[key].Fingerprint && scope == c.PermissionScope(key)
	for _, rule := range rules {
		eligible := c.ServerAllowed(key)
		if rule.Operation == "" {
			eligible = c.ToolAllowed(key, rule.Tool)
		}
		result = append(result, interaction.MCPPermission{Operation: rule.Operation, Tool: rule.Tool, SchemaFingerprint: rule.SchemaFingerprint, Decision: rule.Decision, Eligible: eligible && applicable})
	}
	return result
}

// inspectMCPPermissions is explicit management discovery, outside any model Run.
// It shows exact operation identity without executing a tool or reading a URI.
func inspectMCPPermissions(ctx context.Context, c config.Config, owner *mcpOwner, key string) ([]interaction.MCPPermission, error) {
	server, ok := c.MCP.Servers[key]
	if !ok || owner == nil {
		return nil, fmt.Errorf("MCP service is unavailable")
	}
	bound, ok := owner.configuration.Servers[key]
	if !ok || bound.Fingerprint != server.Fingerprint || owner.configuration.ConnectionDecision(key) != c.MCP.ConnectionDecision(key) || mcpPermissionScope(owner.configuration, key) != mcpPermissionScope(c.MCP, key) || owner.configuration.ServerAllowed(key) != c.MCP.ServerAllowed(key) {
		return nil, fmt.Errorf("MCP connection owner is stale; reopen current configuration")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	connection := owner.Connections()[key]
	catalog, err := connection.Tools(ctx)
	unsupported := errors.Is(err, mcpclient.ErrUnsupported)
	if err != nil && !unsupported || !unsupported && (!catalog.Complete || catalog.Generation != connection.ToolGeneration()) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("MCP permission discovery unavailable; check service status and connection approval")
	}
	encoded, encodeErr := json.Marshal(catalog.Items)
	if encodeErr != nil || len(catalog.Items) > 2000 || len(encoded) > 4<<20 {
		return nil, fmt.Errorf("MCP permission catalog exceeds its bound")
	}
	secrets := mcpKnownSecrets(server, connection)
	var result []interaction.MCPPermission
	permissions := c.MCP.Permissions(key)
	for _, remote := range catalog.Items {
		// Never display a configured credential as an operation selector.
		for _, secret := range secrets {
			if secret != "" && strings.Contains(remote.Name, secret) {
				return nil, fmt.Errorf("MCP operation name contains a configured credential")
			}
		}
		schema := mcpDigest([]json.RawMessage{remote.InputSchema, remote.OutputSchema})
		result = append(result, interaction.MCPPermission{Tool: remote.Name, SchemaFingerprint: schema, Decision: permissions.Decision("", remote.Name, schema), Eligible: c.MCP.ToolAllowed(key, remote.Name)})
	}
	if info, ok := cachedMCPInfo(connection); ok && info.Resources {
		schema := mcpDigest(mcpResourceReadSchema)
		result = append(result, interaction.MCPPermission{Operation: llm.OperationResourceRead, Tool: llm.OperationResourceRead, SchemaFingerprint: schema, Decision: c.MCP.PermissionDecision(key, llm.OperationResourceRead, llm.OperationResourceRead, schema), Eligible: c.MCP.ServerAllowed(key)})
	}
	slices.SortFunc(result, func(a, b interaction.MCPPermission) int {
		if n := strings.Compare(a.Operation, b.Operation); n != 0 {
			return n
		}
		return strings.Compare(a.Tool, b.Tool)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func saveMCPPermission(ctx context.Context, current config.Config, request interaction.MCPRequest) (config.Config, interaction.MCPResult, error) {
	p := request.Permission
	if p == nil || request.PermissionScope != current.MCP.PermissionScope(request.Key) {
		return current, interaction.MCPResult{}, fmt.Errorf("MCP permission scope changed; inspect current permissions")
	}
	rule := config.MCPPermission{Operation: p.Operation, Tool: p.Tool, SchemaFingerprint: p.SchemaFingerprint, Decision: p.Decision}
	candidate, err := current.WithMCPPermission(request.Key, request.Fingerprint, rule)
	if err != nil {
		return current, interaction.MCPResult{}, err
	}
	if rule.Decision == "allow" && (!current.MCP.ServerAllowed(request.Key) || rule.Operation == "" && !current.MCP.ToolAllowed(request.Key, rule.Tool)) {
		return current, interaction.MCPResult{}, fmt.Errorf("MCP configured restrictions cannot be overridden by a user allow rule")
	}
	// This path is deliberately inert: the user supplies the reviewed schema.
	// Only a later current catalog with the exact same schema can use an allow.
	commit, err := config.SaveMCPPermission(ctx, current.Paths, current.MCP, request.Key, rule)
	result := mcpCommitResult(commit, "MCP user permission saved. Connection approval and current catalog checks still apply; ordinary Session approvals remain temporary.")
	if commit.Committed {
		current = candidate
	}
	return current, result, err
}

func prepareMCPPermission(ctx context.Context, c config.Config, owner *mcpOwner, ui *interaction.AuthInteraction, op interaction.MCPRequest) (interaction.MCPRequest, error) {
	decision, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "MCP user permission", Instructions: "These are explicit cross-Session user rules. Connection approval stays separate.", Menu: &interaction.CommandMenu{Title: "Permission decision", Options: []interaction.CommandOption{
		{Label: "Cancel", Arguments: "cancel"}, {Label: "Allow one reviewed operation", Arguments: "allow"}, {Label: "Deny one operation", Arguments: "deny"}, {Label: "Remove a saved rule", Arguments: "ask"},
	}}})
	if err != nil {
		return op, err
	}
	if decision == "cancel" {
		return op, context.Canceled
	}
	permissions := storedMCPPermissions(c.MCP, op.Key)
	if decision != "ask" {
		permissions, err = inspectMCPPermissions(ctx, c, owner, op.Key)
		if err != nil {
			return op, err
		}
	}
	menu := &interaction.CommandMenu{Title: "Select one MCP operation"}
	for i, p := range permissions {
		if decision == "allow" && !p.Eligible {
			continue
		}
		kind := "tool"
		if p.Operation == llm.OperationResourceRead {
			kind = "all resource URIs on this service"
		}
		menu.Options = append(menu.Options, interaction.CommandOption{Label: p.Tool, Description: kind + " · " + p.Decision, Arguments: fmt.Sprint(i)})
	}
	if len(menu.Options) == 0 {
		return op, fmt.Errorf("no matching MCP operations or stored rules are available")
	}
	selected, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: menu.Title, Menu: menu})
	if err != nil {
		return op, err
	}
	var permission interaction.MCPPermission
	for i, p := range permissions {
		if selected == fmt.Sprint(i) {
			permission = p
			break
		}
	}
	permission.Decision = decision
	server := c.MCP.Servers[op.Key]
	op.Fingerprint, op.PermissionScope = server.Fingerprint, c.MCP.PermissionScope(op.Key)
	disclosure := fmt.Sprintf("Service: %s\nSource: %s:%s\nConnection fingerprint: %s\nPermission scope: %s\nOperation: %s %q\nSchema fingerprint: %s\nDecision: %s\nAllow applies only to this exact schema; deny covers schema changes within this binding. Resource permission covers every URI on this service. Saving closes current MCP connections and clears MCP Session approvals.", op.Key, server.Source.Kind, server.Source.Location, op.Fingerprint, op.PermissionScope, permission.Operation, permission.Tool, permission.SchemaFingerprint, decision)
	if decision == "ask" {
		fingerprint, scope, _ := c.MCP.StoredPermissions(op.Key)
		disclosure += fmt.Sprintf("\nRemoving the stored rule at fingerprint %s and scope %s, including when that binding is inactive.", fingerprint, scope)
	}
	choice, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "Confirm MCP user permission", Instructions: disclosure, Menu: &interaction.CommandMenu{Title: "Save permission", Options: []interaction.CommandOption{{Label: "Cancel", Arguments: "cancel"}, {Label: "Save user rule", Arguments: "save"}}}})
	if err != nil {
		return op, err
	}
	if choice != "save" {
		return op, context.Canceled
	}
	op.Permission = &permission
	return op, nil
}
