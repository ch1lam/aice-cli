package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func mcpMenu() *interaction.CommandMenu {
	return &interaction.CommandMenu{Title: "MCP services", Options: []interaction.CommandOption{
		{Label: "Show status", Arguments: "status"},
		{Label: "Add a user service", Arguments: "add"},
		{Label: "Replace a user service definition", Arguments: "replace"},
		{Label: "Approve a connection", Arguments: "approve"},
		{Label: "Deny and revoke a connection", Arguments: "deny"},
		{Label: "Forget connection approval", Arguments: "forget"},
		{Label: "Set or clear a credential", Arguments: "credential"},
		{Label: "Log in to an OAuth service", Arguments: "login"},
		{Label: "Log out of an OAuth service", Arguments: "logout"},
		{Label: "Test connection and discovery", Arguments: "connect"},
		{Label: "Reconnect and discover", Arguments: "reconnect"},
		{Label: "Enable a user service", Arguments: "enable"},
		{Label: "Disable a user service", Arguments: "disable"},
		{Label: "Remove a user service and stored access", Arguments: "remove"},
		{Label: "Inspect operation permissions", Arguments: "permissions"},
		{Label: "Save or remove a user permission", Arguments: "permission"},
	}}
}

func mcpActionKnown(action string) bool {
	for _, option := range mcpMenu().Options {
		if action == option.Arguments {
			return true
		}
	}
	return false
}

// Values remain in the transient interaction. They are never model input,
// command history, a tool call, or a durable Session record.
func mcpPrompt(ctx context.Context, ui *interaction.AuthInteraction, prompt interaction.AuthPrompt) (string, error) {
	if ui == nil || ui.Notify == nil || ui.Input == nil {
		return "", fmt.Errorf("MCP action requires interactive input; use the aice mcp CLI for scripts")
	}
	if err := ui.Notify(ctx, prompt); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value, ok := <-ui.Input:
		if !ok {
			return "", context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(value) > 1<<20 {
			return "", fmt.Errorf("MCP input exceeds the size limit")
		}
		if prompt.Menu != nil {
			for _, option := range prompt.Menu.Options {
				if value == option.Arguments {
					return value, nil
				}
			}
			return "", fmt.Errorf("MCP selection is no longer available")
		}
		return strings.TrimSpace(value), nil
	}
}

func prepareMCPCommand(ctx context.Context, configuration config.Config, owner *mcpOwner, ui *interaction.AuthInteraction, op interaction.MCPRequest) (interaction.MCPRequest, error) {
	if op.Action == "status" {
		return op, nil
	}
	var err error
	if op.Key == "" {
		if op.Action == "add" {
			op.Key, err = mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "MCP service ID", Instructions: "A unique user service ID, for example docs. The reserved cua service is managed separately.", InputLabel: "Service ID", AllowInput: true, PublicInput: true})
		} else {
			menu := &interaction.CommandMenu{Title: "Select the exact MCP service"}
			for _, service := range mcpManagementStatus(configuration, owner, "") {
				server := configuration.MCP.Servers[service.Key]
				if (op.Action == "replace" || op.Action == "enable" || op.Action == "disable" || op.Action == "remove") && server.Source.Kind != "user" {
					continue
				}
				menu.Options = append(menu.Options, interaction.CommandOption{Label: service.Key, Description: service.Source, Arguments: service.Key})
			}
			if len(menu.Options) == 0 {
				return op, fmt.Errorf("no editable MCP services are available for this action")
			}
			op.Key, err = mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: menu.Title, Menu: menu})
		}
		if err != nil {
			return op, err
		}
	}
	if op.Action == "add" || op.Action == "replace" {
		definition, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "MCP server definition (JSON)", InputLabel: "One JSON object", AllowInput: true, PublicInput: true, Instructions: `Paste a single-line server definition, e.g. {"transport":"http","url":"https://example.com/mcp"}. For stdio use an absolute command and cwd. Use env/auth_ref references for credentials; enter secret values through the credential action. Saving does not connect.`})
		op.Definition = json.RawMessage(definition)
		return op, err
	}
	server, exists := configuration.MCP.Servers[op.Key]
	if !exists {
		return op, fmt.Errorf("MCP service key is not configured")
	}
	if op.Action == "permission" {
		return prepareMCPPermission(ctx, configuration, owner, ui, op)
	}
	op.Fingerprint = server.Fingerprint
	definition, _ := json.MarshalIndent(server.Settings, "", "  ")
	disclosure := fmt.Sprintf("Service: %s\nSource: %s:%s\nConnection fingerprint: %s\nConfigured connection (references only for credentials):\n%s\n", op.Key, server.Source.Kind, server.Source.Location, server.Fingerprint, definition)
	switch op.Action {
	case "approve", "deny", "forget", "remove", "login", "logout":
		explanation := map[string]string{
			"login":   "Contact the configured service and its discovered authorization server, register a public client if needed, and open consent in your browser. A successful login replaces the old login and removes its connection approval and user tool rules. Tool permission is separate.",
			"logout":  "Remove this service's OAuth login, connection approval and user tool rules, close current MCP connections and clear MCP Session tool grants. Explicit client-secret slots remain configured.",
			"approve": "Allow this exact connection to start its configured process or contact its endpoint. Remote tool execution still requires separate permission.",
			"deny":    "Deny this connection and cancel its active work now. Already dispatched actions may have taken effect; they will not be replayed.",
			"forget":  "Remove the stored connection decision, close current MCP connections, and clear MCP Session tool grants. --yolo may still allow connections without a stored decision.",
			"remove":  "Remove this user definition, its stored credentials, connection decision and user tool rules. Project definitions are read-only.",
		}[op.Action]
		choice, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "Confirm MCP " + op.Action, Instructions: disclosure + explanation, Menu: &interaction.CommandMenu{Title: "Confirm action", Options: []interaction.CommandOption{{Label: "Cancel", Arguments: "cancel"}, {Label: "Confirm " + op.Action, Arguments: "confirm"}}}})
		if err != nil {
			return op, err
		}
		if choice != "confirm" {
			return op, context.Canceled
		}
	case "credential":
		slots := server.CredentialSlots()
		if len(slots) == 0 {
			return op, fmt.Errorf("service has no auth_ref slots; replace its definition first")
		}
		menu := &interaction.CommandMenu{Title: "Credential slot"}
		for _, slot := range slots {
			menu.Options = append(menu.Options, interaction.CommandOption{Label: slot, Arguments: slot})
		}
		op.Slot, err = mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: menu.Title, Instructions: disclosure, Menu: menu})
		if err != nil {
			return op, err
		}
		choice, err := mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "Set or clear credential", Menu: &interaction.CommandMenu{Title: "Credential action", Options: []interaction.CommandOption{{Label: "Enter a new value", Arguments: "set"}, {Label: "Clear stored value", Arguments: "clear"}}}})
		if err != nil {
			return op, err
		}
		if choice == "set" {
			op.Secret, err = mcpPrompt(ctx, ui, interaction.AuthPrompt{Title: "MCP credential for " + op.Key + " / " + op.Slot, InputLabel: "Credential (input hidden)", AllowInput: true, Instructions: "Saved only to this service's credential scope. A changed fingerprint needs new connection approval."})
			if err == nil && (op.Secret == "" || len(op.Secret) > 8192) {
				err = fmt.Errorf("MCP credential must contain 1–8192 bytes")
			}
			if err != nil {
				return op, err
			}
		}
	}
	return op, nil
}
