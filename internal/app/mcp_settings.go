package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// runMCPSettings owns one settings reservation, including transient prompts.
// Deny may revoke a live service; operations that replace resources require idle.
func (s *interactiveSession) runMCPSettings(ctx context.Context, revision *uint64, request interaction.CommandRequest) (result interaction.SettingsActionResult, returnErr error) {
	fields := strings.Fields(request.Arguments)
	if len(fields) > 2 || request.Secret != "" {
		return result, fmt.Errorf("usage: /mcp [action] [service]; enter credentials only in the private prompt")
	}
	op := interaction.MCPRequest{Action: "status"}
	if len(fields) > 0 {
		op.Action = fields[0]
	}
	if len(fields) > 1 {
		op.Key = fields[1]
	}
	if len(fields) == 0 && request.Auth != nil {
		var err error
		op.Action, err = mcpPrompt(ctx, request.Auth, interaction.AuthPrompt{Title: "MCP services", Menu: mcpMenu()})
		if err != nil {
			return result, err
		}
	}
	if !mcpActionKnown(op.Action) {
		return result, fmt.Errorf("unknown MCP action; use /mcp to choose an action")
	}
	shared := op.Action != "status" && op.Action != "deny"
	if err := s.beginSettingsOperation(revision, shared); err != nil {
		return result, err
	}
	changed := false
	defer func() {
		var warnings []string
		result.Revision, warnings = s.endSettingsOperation(changed)
		result.Warnings = append(result.Warnings, warnings...)
	}()
	s.stateMu.RLock()
	current, owner := s.configuration, s.mcp
	s.stateMu.RUnlock()
	var err error
	op, err = prepareMCPCommand(ctx, current, owner, request.Auth, op)
	if err != nil {
		return result, err
	}
	if op.Action == "reconnect" {
		changed = true
		if err := s.publishMCPConfiguration(current); err != nil {
			return result, err
		}
		s.stateMu.RLock()
		owner = s.mcp
		s.stateMu.RUnlock()
	}
	next, managed, err := executeMCPManagement(ctx, current, owner, op, s.application.mcpLogin(request.Auth, false))
	result.Committed = managed.Committed
	result.Warnings = append(result.Warnings, managed.Warnings...)
	changed = changed || managed.Committed || op.Action == "connect"
	if managed.Committed {
		if op.Action == "deny" {
			// Publish the persisted decision even if writer cleanup failed. The
			// owner is immutable: revoke its capability rather than mutating it.
			s.stateMu.Lock()
			s.configuration = next
			s.stateMu.Unlock()
			if owner != nil {
				err = errors.Join(err, owner.Revoke(op.Key))
			}
			result.Applied = true
		} else {
			applyErr := s.publishMCPConfiguration(next)
			result.Applied = applyErr == nil
			err = errors.Join(err, applyErr)
		}
	}
	result.Output = managed.Message
	if err == nil && (op.Action == "connect" || op.Action == "reconnect") {
		result.Output = "MCP " + op.Action + " completed.\n" + result.Output
	}
	if op.Action == "status" || op.Action == "connect" || op.Action == "reconnect" || op.Action == "permissions" {
		result.Output = strings.TrimSpace(result.Output + "\n" + formatMCPStatus(managed.Services))
	}
	if op.Action == "permissions" {
		for _, p := range managed.Permissions {
			result.Output += fmt.Sprintf("\n%s %q · %s · eligible=%t\nSchema: %s", p.Operation, p.Tool, p.Decision, p.Eligible, p.SchemaFingerprint)
		}
	}
	return result, err
}

// publishMCPConfiguration is called only under an idle resource reservation.
// A replacement closes every old transport and clears MCP Session grants; the
// next run starts with new catalogs. Other tool permissions are untouched.
func (s *interactiveSession) publishMCPConfiguration(next config.Config) error {
	s.stateMu.RLock()
	old := s.mcp
	s.stateMu.RUnlock()
	tools, err := composeTools(s.baseTools, s.web, s.desktop, next)
	model, options, modelErr := resolveModelSettings(s.providers, next)
	prompt := ""
	if err == nil {
		prompt, err = assembleSystemPrompt(s.workspace, next, s.trustDecision, tools, s.skills)
	}
	var loop *agent.Loop
	if err == nil && s.application != nil && modelErr == nil && providerConfigured(s.providers, next) {
		loop, err = s.application.newAgentLoopWithOptions(next, tools, agent.WithGuard(s.guardAdapter), agent.WithGuardAskHandler(s.handleGuardAsk))
	}
	if closeErr := old.Close(); closeErr != nil {
		s.settingsWarning(fmt.Errorf("owned MCP connection cleanup failed"))
	}
	if err == nil && (s.guard == nil || s.guardAdapter == nil) {
		err = fmt.Errorf("MCP Guard is unavailable")
	}
	var owner *mcpOwner
	if err == nil {
		if old != nil {
			for _, server := range old.configuration.Servers {
				s.guard.RemoveMCPService(server.Source.Kind+":"+server.Source.Location, server.ID)
			}
		}
		var open mcpOpenFunc
		if s.application != nil {
			open = s.application.dependencies.openMCP
		}
		owner, err = newMCPOwner(next.MCP, s.guard, s.guardAdapter.yolo, open, mcpOAuthRefresh(next.Paths))
	}
	s.stateMu.Lock()
	s.configuration = next
	if err == nil {
		s.mcp, s.tools, s.systemPrompt, s.loop = owner, tools, prompt, loop
		s.modelErr = modelErr
		if modelErr == nil {
			s.model, s.options = applyContextWindow(model, next), options
		}
	} else {
		// A saved narrower policy must never leave the old executable runtime
		// available. Preserve the closed owner for cleanup and stop new runs.
		s.loop = nil
		s.modelErr = fmt.Errorf("MCP runtime update failed; restart AICE: %w", err)
	}
	s.stateMu.Unlock()
	return err
}

func formatMCPStatus(services []interaction.MCPService) string {
	if len(services) == 0 {
		return "No MCP services configured. Use /mcp add."
	}
	var b strings.Builder
	for _, service := range services {
		fmt.Fprintf(&b, "%s · %s · connection %s\nSource: %s\nFingerprint: %s\n", service.Key, service.State, service.Approval, service.Source, service.Fingerprint)
		if service.CatalogKnown {
			fmt.Fprintf(&b, "Tools: %d discovered, %d eligible\n", service.ToolCount, service.EligibleTools)
		} else {
			b.WriteString("Tools: catalog not currently known\n")
		}
		fmt.Fprintf(&b, "Permission scope: %s\n", service.PermissionScope)
		if len(service.Permissions) > 0 {
			fmt.Fprintf(&b, "Saved rule binding: %s · %s\n", service.SavedPermissionFingerprint, service.SavedPermissionScope)
			if service.SavedPermissionFingerprint != service.Fingerprint || service.SavedPermissionScope != service.PermissionScope {
				b.WriteString("Saved rules are inactive for the current binding.\n")
			}
		}
		for _, rule := range service.Permissions {
			fmt.Fprintf(&b, "Saved %s: %s %q · schema %s\n", rule.Decision, rule.Operation, rule.Tool, rule.SchemaFingerprint)
		}
		if service.Detail != "" {
			b.WriteString(service.Detail + "\n")
		}
	}
	b.WriteString("Tool selection belongs to each run; a ready connection does not grant tool execution permission.")
	return b.String()
}
