package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	shared := op.Action != "status" && op.Action != "deny" && op.Action != "desktop"
	if err := s.beginSettingsOperation(revision, shared); err != nil {
		return result, err
	}
	changed := false
	defer func() {
		var warnings []string
		result.Revision, warnings = s.endSettingsOperation(changed, changed && shared)
		result.Warnings = append(result.Warnings, warnings...)
	}()
	if op.Action == "desktop" {
		if op.Key != "" {
			return result, fmt.Errorf("usage: /mcp desktop")
		}
		result.FocusSetting = "desktop_enabled"
		result.Output = "Computer Use is managed in /desktop."
		return result, nil
	}
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
		if err := s.publishMCPConfiguration(current, op.Key); err != nil {
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
			// active service is revoked without replacing a live Run's resources.
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
		s.applyMCPLoadedTools(managed.Services)
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
// Only changed services lose transports and Session grants. A global restriction
// change also invalidates managed Computer Use authority, including on failure.
func (s *interactiveSession) publishMCPConfiguration(next config.Config, reconnect ...string) error {
	s.stateMu.RLock()
	old := s.mcp
	previous := s.configuration.MCP
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
	if !reflect.DeepEqual(previous.Restrictions, next.MCP.Restrictions) {
		s.desktop.invalidateManagedCatalog(s.guard)
	}
	if err == nil && (s.guard == nil || s.guardAdapter == nil) {
		err = fmt.Errorf("MCP Guard is unavailable")
	}
	owner := old
	if old != nil {
		// Even if model/prompt preparation failed, retire the changed capabilities
		// so a saved narrower policy cannot leave old references executable.
		cleanupErr, applyErr := old.reconfigure(next.MCP, reconnect...)
		if cleanupErr != nil {
			s.settingsWarning(fmt.Errorf("owned MCP connection cleanup failed"))
		}
		err = errors.Join(err, applyErr)
	} else if err == nil {
		if s.application != nil {
			owner, err = s.application.newConfiguredMCPOwner(next, s.guard, s.guardAdapter.yolo)
		} else {
			err = fmt.Errorf("MCP application is unavailable")
		}
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
		// available. Unchanged connections remain owned; stop new runs until repair.
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
	for i, service := range services {
		if i > 0 {
			b.WriteString("\n")
		}
		loaded := "Loaded tools: 0 (no active main Run)\n"
		if service.RunActive {
			loaded = fmt.Sprintf("Loaded tools: %d (latest model request in current Run)\n", service.LoadedTools)
		}
		if service.Managed {
			fmt.Fprintf(&b, "Service: %s\nConnection\nState: %s\nManaged by: Computer Use (/desktop)\n%s\n\nTool catalog\n", service.Key, service.State, service.Detail)
			b.WriteString(loaded)
			continue
		}
		fmt.Fprintf(&b, "Service: %s\nConnection\nState: %s\nApproval: %s\nSource: %s\nFingerprint: %s\n\nTool catalog\n",
			service.Key, service.State, service.Approval, service.Source, service.Fingerprint)
		if service.CatalogKnown {
			fmt.Fprintf(&b, "Tools: %d discovered, %d eligible\n", service.ToolCount, service.EligibleTools)
		} else {
			b.WriteString("Tools: catalog not currently known\n")
		}
		b.WriteString(loaded)
		fmt.Fprintf(&b, "\nPermissions\nPermission scope: %s\n", service.PermissionScope)
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
			b.WriteString("\nDiagnostics\n" + service.Detail + "\n")
		}
	}
	b.WriteString("\nNotes\nLoaded counts describe the latest request; revocation still blocks execution immediately. A ready connection does not grant tool execution permission.")
	return b.String()
}
