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
	tools, prompt, err := s.sessionTools(next, s.web)
	model, options, modelErr := resolveModelSettings(s.providers, next)
	var loop *agent.Loop
	if err == nil && s.application != nil && modelErr == nil && providerConfigured(s.providers, next) {
		loop, err = s.sessionLoop(next, tools)
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
	return interaction.DetailsText(mcpStatusDetails(services))
}

// mcpStatusDetails describes each service's connection, catalog and
// permissions; the application decides each state's tone.
func mcpStatusDetails(services []interaction.MCPService) []interaction.Detail {
	if len(services) == 0 {
		return []interaction.Detail{{Text: "No MCP services configured. Use /mcp add."}}
	}
	var details []interaction.Detail
	add := func(key, value string, tone interaction.DetailTone) {
		details = append(details, interaction.Detail{Key: key, Value: value, Tone: tone})
	}
	heading := func(name string) { details = append(details, interaction.Detail{Heading: name}) }
	text := func(line string) { details = append(details, interaction.Detail{Text: line}) }
	for i, service := range services {
		if i > 0 {
			text("")
		}
		loaded := "0 (no active main Run)"
		if service.RunActive {
			loaded = fmt.Sprintf("%d (latest model request in current Run)", service.LoadedTools)
		}
		add("Service", service.Key, interaction.ToneTitle)
		heading("Connection")
		add("State", service.State, mcpStateTone(service.State))
		if service.Managed {
			add("Managed by", "Computer Use (/desktop)", interaction.ToneDefault)
			text(service.Detail)
			text("")
			heading("Tool catalog")
			add("Loaded tools", loaded, interaction.ToneDefault)
			continue
		}
		add("Approval", service.Approval, mcpStateTone(service.Approval))
		add("Source", service.Source, interaction.ToneReference)
		add("Fingerprint", service.Fingerprint, interaction.ToneReference)
		text("")
		heading("Tool catalog")
		if service.CatalogKnown {
			add("Tools", fmt.Sprintf("%d discovered, %d eligible", service.ToolCount, service.EligibleTools), interaction.ToneDefault)
		} else {
			add("Tools", "catalog not currently known", interaction.ToneDefault)
		}
		add("Loaded tools", loaded, interaction.ToneDefault)
		text("")
		heading("Permissions")
		add("Permission scope", service.PermissionScope, interaction.ToneDefault)
		if len(service.Permissions) > 0 {
			add("Saved rule binding", service.SavedPermissionFingerprint+" · "+service.SavedPermissionScope, interaction.ToneReference)
			if service.SavedPermissionFingerprint != service.Fingerprint || service.SavedPermissionScope != service.PermissionScope {
				text("Saved rules are inactive for the current binding.")
			}
		}
		for _, rule := range service.Permissions {
			add("Saved "+rule.Decision, fmt.Sprintf("%s %q · schema %s", rule.Operation, rule.Tool, rule.SchemaFingerprint), interaction.ToneDefault)
		}
		if service.Detail != "" {
			text("")
			heading("Diagnostics")
			text(service.Detail)
		}
	}
	text("")
	heading("Notes")
	text("Loaded counts describe the latest request; revocation still blocks execution immediately. A ready connection does not grant tool execution permission.")
	return details
}

func mcpStateTone(state string) interaction.DetailTone {
	switch state {
	case "ready", "allow":
		return interaction.ToneGood
	case "failed", "deny":
		return interaction.ToneBad
	case "needs_auth", "connecting", "ask":
		return interaction.ToneWarning
	}
	return interaction.ToneMuted
}
