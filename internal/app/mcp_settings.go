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
		result.Revision, warnings = s.endSettingsOperation(changed, changed && shared)
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
		result.Output = strings.TrimSpace("MCP " + op.Action + " completed.\n" + result.Output)
	}
	if op.Action == "status" || op.Action == "connect" || op.Action == "reconnect" || op.Action == "permissions" {
		s.applyMCPLoadedTools(managed.Services)
		result.Details = mcpStatusDetails(managed.Services)
	}
	if op.Action == "permissions" && len(managed.Permissions) > 0 {
		result.Details = append(result.Details, interaction.Detail{}, interaction.Detail{Heading: "Operation permissions"})
		for _, p := range managed.Permissions {
			result.Details = append(result.Details, interaction.Detail{Indent: 2, Key: fmt.Sprintf("%s %q", p.Operation, p.Tool),
				Value: fmt.Sprintf("%s · eligible=%t · schema %s", p.Decision, p.Eligible, p.SchemaFingerprint), Tone: mcpStateTone(p.Decision)})
		}
	}
	return result, err
}

// publishMCPConfiguration is called only under an idle resource reservation.
// Only changed services lose transports and Session grants.
func (s *interactiveSession) publishMCPConfiguration(next config.Config, reconnect ...string) error {
	s.stateMu.RLock()
	old := s.mcp
	s.stateMu.RUnlock()
	tools, prompt, err := s.sessionTools(next, s.web)
	model, options, modelErr := resolveModelSettings(s.providers, next)
	var loop *agent.Loop
	if err == nil && s.application != nil && modelErr == nil && providerConfigured(s.providers, next) {
		loop, err = s.sessionLoop(next, tools)
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

// mcpStatusDetails describes every service for /mcp output; the application
// decides each state's tone.
func mcpStatusDetails(services []interaction.MCPService) []interaction.Detail {
	if len(services) == 0 {
		return []interaction.Detail{{Text: "No MCP services configured. Use /mcp add."}}
	}
	var details []interaction.Detail
	for i, service := range services {
		if i > 0 {
			details = append(details, interaction.Detail{})
		}
		details = append(details, interaction.Detail{Heading: service.Key})
		for _, detail := range mcpServiceDetails(service) {
			detail.Indent += 2
			details = append(details, detail)
		}
	}
	return append(details, interaction.Detail{}, interaction.Detail{Tone: interaction.ToneMuted,
		Text: "Loaded counts describe the latest request; revocation still blocks execution immediately. A ready connection does not grant tool execution permission."})
}

// mcpServiceDetails lists one service's state first, then its catalog,
// permissions and identity, so the useful facts lead a short header.
func mcpServiceDetails(service interaction.MCPService) []interaction.Detail {
	state, tone := mcpServiceSummary(service)
	tools := "catalog not yet known"
	if service.CatalogKnown {
		tools = fmt.Sprintf("%d discovered · %d eligible", service.ToolCount, service.EligibleTools)
	}
	if service.RunActive {
		tools += fmt.Sprintf(" · %d loaded in this Run", service.LoadedTools)
	}
	details := []interaction.Detail{
		{Key: "State", Value: state, Tone: tone},
		{Key: "Approval", Value: service.Approval, Tone: mcpStateTone(service.Approval)},
		{Key: "Tools", Value: tools},
		{Key: "Permissions", Value: service.PermissionScope, Tone: interaction.ToneReference},
		{Key: "Source", Value: service.Source, Tone: interaction.ToneReference},
		{Key: "Fingerprint", Value: service.Fingerprint, Tone: interaction.ToneReference},
	}
	if len(service.Permissions) > 0 {
		details = append(details, interaction.Detail{}, interaction.Detail{Heading: "Saved rules"})
		if service.SavedPermissionFingerprint != service.Fingerprint || service.SavedPermissionScope != service.PermissionScope {
			details = append(details, interaction.Detail{Text: "Inactive: saved for " + service.SavedPermissionFingerprint + " · " + service.SavedPermissionScope, Tone: interaction.ToneWarning})
		}
		for _, rule := range service.Permissions {
			details = append(details, interaction.Detail{Key: rule.Decision, Value: fmt.Sprintf("%s %q · schema %s", rule.Operation, rule.Tool, rule.SchemaFingerprint), Tone: mcpStateTone(rule.Decision)})
		}
	}
	if service.Detail != "" {
		details = append(details, interaction.Detail{}, interaction.Detail{Heading: "Diagnostics"}, interaction.Detail{Text: service.Detail, Tone: tone})
	}
	return details
}

// mcpServiceSummary is the short status a Settings row shows for a service.
func mcpServiceSummary(service interaction.MCPService) (string, interaction.DetailTone) {
	if !service.Enabled {
		return "disabled", interaction.ToneMuted
	}
	state := strings.ReplaceAll(service.State, "_", " ")
	if service.State == "disconnected" {
		state = "not connected"
	}
	if service.State == "ready" && service.CatalogKnown {
		state += fmt.Sprintf(" · %d tools", service.ToolCount)
	}
	return state, mcpStateTone(service.State)
}

func mcpStateTone(state string) interaction.DetailTone {
	switch state {
	case "ready", "allow":
		return interaction.ToneGood
	case "failed", "deny", "denied":
		return interaction.ToneBad
	case "needs_auth", "needs_approval", "connecting", "ask":
		return interaction.ToneWarning
	}
	return interaction.ToneMuted
}

// mcpSettingFields lists each service with only the actions its state and
// source allow, then the entry that adds a user service.
func mcpSettingFields(configuration config.Config, services []interaction.MCPService, reason string) []interaction.SettingField {
	fields := make([]interaction.SettingField, 0, len(services)+1)
	for _, service := range services {
		server := configuration.MCP.Servers[service.Key]
		status, tone := mcpServiceSummary(service)
		fields = append(fields, interaction.SettingField{
			ID: "mcp.service." + service.Key, Category: "tools", Label: service.Key, Keywords: []string{"mcp", service.Name},
			Description: "Connection, credentials and tool permissions of this " + server.Source.Kind + " MCP service.",
			Kind:        interaction.SettingAction, Value: interaction.SettingValue{Text: status}, Tone: tone, Details: mcpServiceDetails(service),
			Action:  &interaction.Command{Name: "mcp", Interactive: true, Menu: mcpServiceMenu(server, service)},
			Applies: interaction.SettingDomainAction, DisabledReason: reason,
		})
	}
	return append(fields, interaction.SettingField{
		ID: "mcp.add", Category: "tools", Label: "Add service…", Keywords: []string{"mcp"},
		Description: "Add a user MCP service from a JSON definition. Saving does not connect.",
		Kind:        interaction.SettingAction, Action: &interaction.Command{Name: "mcp", Interactive: true}, Arguments: "add",
		Applies: interaction.SettingDomainAction, DisabledReason: reason,
	})
}

func mcpServiceMenu(server config.MCPServer, service interaction.MCPService) *interaction.CommandMenu {
	menu := &interaction.CommandMenu{Title: service.Key}
	add := func(label, action, description string) {
		menu.Options = append(menu.Options, interaction.CommandOption{Label: label, Arguments: action + " " + service.Key, Description: description})
	}
	user := server.Source.Kind == "user"
	approved := service.Approval == string(config.MCPConnectionAllow)
	switch {
	case !service.Enabled:
		if user {
			add("Enable", "enable", "")
		}
	case approved:
		add("Test connection", "connect", "Connect and discover tools")
		add("Reconnect", "reconnect", "Restart the connection and rediscover")
	default:
		add("Approve connection", "approve", "Allow this exact connection to start")
	}
	if server.Settings.OAuth != nil {
		if _, loggedIn := server.OAuthCredentials(); loggedIn {
			add("Log out", "logout", "Remove the OAuth login, its approval and tool rules")
		} else {
			add("Log in", "login", "Authorize in your browser")
		}
	}
	if len(server.CredentialSlots()) > 0 {
		add("Set or clear a credential", "credential", "")
	}
	add("Inspect tool permissions", "permissions", "")
	add("Save or remove a tool permission", "permission", "")
	if service.Approval != string(config.MCPConnectionDeny) {
		add("Deny and revoke", "deny", "Cancel active work and block connections")
	}
	if service.Approval != string(config.MCPConnectionAsk) {
		add("Forget approval", "forget", "Ask again before the next connection")
	}
	if user {
		add("Replace definition", "replace", "")
		if service.Enabled {
			add("Disable", "disable", "")
		}
		add("Remove", "remove", "Delete the definition and its stored access")
	}
	return menu
}
