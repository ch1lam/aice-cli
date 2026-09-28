package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// ManageMCP loads configuration under Project Trust, without model creation,
// helper installation, Session creation or initializing optional services.
func (a *application) ManageMCP(ctx context.Context, request cli.MCPRequest) (result interaction.MCPResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	workspace, err := tool.NewWorkspace(request.Workspace)
	if err != nil {
		return result, err
	}
	configuration, err := a.dependencies.loadConfig(config.LoadOptions{
		Workspace: workspace.PhysicalPath(),
		TrustProject: func(paths config.Paths, policy trust.Default) (bool, error) {
			choice, err := a.chooseProjectTrust(workspace, config.Config{Paths: paths, DefaultProjectTrust: policy}, request.ProjectTrustOverride, nil)
			return choice.Decision == trust.DecisionTrusted, err
		},
	})
	if err != nil {
		return result, err
	}
	gate, _, err := newExecutionGuard(workspace.PhysicalPath(), nil, false)
	if err != nil {
		return result, err
	}
	owner, err := newMCPOwner(configuration.MCP, gate, false, a.dependencies.openMCP)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := owner.Close(); err != nil {
			result.Warnings = append(result.Warnings, "Owned MCP connection cleanup failed")
		}
	}()
	_, result, returnErr = executeMCPManagement(ctx, configuration, owner, request.Operation)
	return result, returnErr
}

// executeMCPManagement is shared application behavior for management frontends.
// It returns the exact saved configuration so an interactive coordinator can
// publish it under its existing idle/settings reservation. It never grants tool
// permissions and never writes a transcript. Test and save are separate actions.
func executeMCPManagement(ctx context.Context, current config.Config, owner *mcpOwner, request interaction.MCPRequest) (config.Config, interaction.MCPResult, error) {
	result := interaction.MCPResult{}
	if err := ctx.Err(); err != nil {
		return current, result, err
	}
	if err := validateMCPManagement(request); err != nil {
		return current, result, err
	}
	key := request.Key
	if request.Action == "add" {
		key = "user:" + key
	}
	server, exists := current.MCP.Servers[key]
	if request.Action != "status" && request.Action != "add" && !exists {
		return current, result, fmt.Errorf("MCP service key is not configured; use a source-qualified key from status")
	}
	if request.Action == "status" {
		if key != "" && !exists {
			return current, result, fmt.Errorf("MCP service key is not configured")
		}
		result.Services = mcpManagementStatus(current, owner, key)
		return current, result, nil
	}
	if request.Action == "connect" || request.Action == "reconnect" {
		if owner == nil {
			return current, result, fmt.Errorf("MCP connection owner is unavailable")
		}
		bound, ok := owner.configuration.Servers[key]
		if !ok || bound.Fingerprint != server.Fingerprint ||
			owner.configuration.ConnectionDecision(key) != current.MCP.ConnectionDecision(key) ||
			owner.configuration.ServerAllowed(key) != current.MCP.ServerAllowed(key) ||
			mcpPermissionScope(owner.configuration, key) != mcpPermissionScope(current.MCP, key) {
			return current, result, fmt.Errorf("MCP connection owner is stale; rebind the current configuration before testing")
		}
		// CLI invocations always own a fresh transport; interactive reconnect will
		// replace its owner before invoking this same explicit discovery operation.
		catalog, err := owner.Connections()[key].Tools(ctx)
		result.Services = mcpManagementStatus(current, owner, key)
		if err != nil {
			if ctx.Err() != nil {
				return current, result, ctx.Err()
			}
			return current, result, fmt.Errorf("MCP connection test failed; configuration was not changed; inspect service status")
		}
		if !catalog.Complete {
			return current, result, fmt.Errorf("MCP connection established but tool discovery is incomplete")
		}
		result.Message = "MCP initialization and tool discovery succeeded; no remote tool was called."
		return current, result, nil
	}
	if request.Action == "approve" || request.Action == "deny" || request.Action == "forget" || request.Action == "credential" {
		if request.Fingerprint == "" || request.Fingerprint != server.Fingerprint {
			return current, result, fmt.Errorf("MCP connection changed or fingerprint missing; inspect status and confirm the current connection")
		}
		if request.Action == "credential" {
			// A slot must already be referenced by this service. This prevents a typo
			// from silently saving an unused secret or borrowing a provider namespace.
			referenced := false
			for _, refs := range []map[string]config.MCPValueRef{server.Settings.Env, server.Settings.Headers} {
				for _, ref := range refs {
					referenced = referenced || ref.AuthRef == request.Slot
				}
			}
			if !referenced {
				return current, result, fmt.Errorf("MCP credential slot is not referenced by this service")
			}
			candidate, err := current.WithMCPCredential(key, server.CredentialScope, request.Slot, request.Secret)
			if err != nil {
				return current, result, err
			}
			commit, err := config.SaveMCPCredential(ctx, current.Paths, server, request.Slot, request.Secret)
			result = mcpCommitResult(commit, "MCP credential saved; a changed connection fingerprint requires approval again.")
			if commit.Committed {
				current = candidate
			}
			return current, result, err
		}
		decision := config.MCPConnectionAsk
		if request.Action == "approve" {
			decision = config.MCPConnectionAllow
		}
		if request.Action == "deny" {
			decision = config.MCPConnectionDeny
		}
		candidate, err := current.WithMCPConnectionDecision(key, server.Fingerprint, decision)
		if err != nil {
			return current, result, err
		}
		commit, err := config.SaveMCPConnectionDecision(ctx, current.Paths, server, decision)
		result = mcpCommitResult(commit, "MCP connection decision saved; tool execution permissions are separate.")
		if commit.Committed {
			current = candidate
		}
		return current, result, err
	}
	patch := config.MCPPatch{}
	switch request.Action {
	case "add", "replace":
		if request.Action == "add" && exists {
			return current, result, fmt.Errorf("MCP user service already exists; use replace explicitly")
		}
		if request.Action == "replace" && server.Source.Kind != "user" {
			return current, result, fmt.Errorf("project MCP definitions are read-only; edit the source configuration")
		}
		var definition config.MCPServerSettings
		if len(request.Definition) > 1<<20 || jsonutil.DecodeStrict(request.Definition, &definition) != nil {
			return current, result, fmt.Errorf("invalid MCP server definition (values omitted)")
		}
		id := server.ID
		if request.Action == "add" {
			id = request.Key
		}
		patch.Servers = map[string]*config.MCPServerSettings{id: &definition}
		patch.CreateOnly = request.Action == "add"
	case "enable", "disable", "remove":
		if server.Source.Kind != "user" {
			return current, result, fmt.Errorf("project MCP definitions are read-only; use connection deny or edit the source configuration")
		}
		if request.Action == "remove" {
			patch.Servers = map[string]*config.MCPServerSettings{server.ID: nil}
		} else {
			patch.Enabled = map[string]bool{server.ID: request.Action == "enable"}
		}
	default:
		return current, result, fmt.Errorf("unsupported MCP management action")
	}
	// Remove access first. The two existing files have independent atomic
	// writers; if the second write fails, report the committed access removal
	// explicitly instead of claiming an all-or-nothing cross-file transaction.
	accessRemoved := false
	if request.Action == "remove" {
		candidate, err := current.WithoutMCPServiceAccess(key)
		if err != nil {
			return current, result, err
		}
		commit, err := config.ForgetMCPServiceAccess(ctx, current.Paths, server)
		if commit.Committed {
			current = candidate
			accessRemoved = true
		}
		result = mcpCommitResult(commit, "Stored MCP credentials and connection decisions removed.")
		if err != nil {
			return current, result, err
		}
	}
	saved, commit, err := config.SaveMCPPatch(ctx, current.Paths, patch)
	warnings := result.Warnings
	result = mcpCommitResult(commit, "MCP configuration saved without connecting; connection approval and tool permissions are separate.")
	result.Warnings = append(warnings, result.Warnings...)
	if accessRemoved && !commit.Committed {
		result.Committed = true
		result.Message = "Stored MCP access was removed, but the service definition was not removed."
	}
	if commit.Committed {
		candidate, applyErr := current.WithMCP(saved)
		if applyErr != nil {
			return current, result, fmt.Errorf("MCP configuration was saved but runtime projection failed: %w", applyErr)
		}
		current = candidate
	}
	return current, result, err
}

func validateMCPManagement(r interaction.MCPRequest) error {
	if len(r.Key) > 256 || strings.ContainsAny(r.Key, "\x00\r\n") || len(r.Fingerprint) > 64 {
		return fmt.Errorf("invalid MCP management identity")
	}
	if r.Action != "status" && r.Key == "" {
		return fmt.Errorf("MCP service identity is required")
	}
	if r.Action != "add" && r.Action != "replace" && len(r.Definition) > 0 {
		return fmt.Errorf("MCP definition is only valid for add or replace")
	}
	if r.Action != "credential" && (r.Secret != "" || r.Slot != "") {
		return fmt.Errorf("MCP credential input is only valid for credential management")
	}
	if r.Action == "credential" && r.Slot == "" {
		return fmt.Errorf("MCP credential slot is required")
	}
	return nil
}

func mcpCommitResult(commit config.CommitResult, message string) interaction.MCPResult {
	result := interaction.MCPResult{Committed: commit.Committed}
	if commit.Committed {
		result.Message = message
	}
	if commit.CleanupWarning != nil {
		result.Warnings = append(result.Warnings, "Saved, but configuration lock cleanup failed")
	}
	return result
}

func mcpManagementStatus(configuration config.Config, owner *mcpOwner, key string) []interaction.MCPService {
	statuses := make(map[string]mcpServiceStatus)
	if owner != nil {
		for _, status := range owner.Status() {
			statuses[status.Key] = status
		}
	}
	var result []interaction.MCPService
	for _, id := range configuration.MCP.ServerKeys() {
		if key != "" && key != id {
			continue
		}
		server := configuration.MCP.Servers[id]
		status := statuses[id]
		if status.State == "" {
			status.State = "disconnected"
		}
		definition, _ := json.Marshal(server.Settings)
		result = append(result, interaction.MCPService{Key: id, Name: server.Settings.Name, Source: server.Source.Kind + ":" + server.Source.Location, State: status.State, Detail: status.Detail, Fingerprint: server.Fingerprint, Approval: string(configuration.MCP.ConnectionDecision(id)), Definition: definition, ToolCount: status.ToolCount, CatalogKnown: status.CatalogKnown, EligibleTools: status.EligibleTools})
	}
	return result
}
