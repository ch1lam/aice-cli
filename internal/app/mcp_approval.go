package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

const (
	guardOptionAllowMCPConnection       = "allow-mcp-connection"
	guardOptionAllowMCPConnectionSaved  = "allow-mcp-connection-saved"
	guardRuleMCPConnection              = "mcp.connection"
	mcpConnectionApprovalCommandMaxSize = 1024
)

type mcpConnectionApproverKey struct{}

// mcpConnectionApprover asks the user whether one exact connection may start.
// It returns true only for an explicit allow and persists a saved choice itself.
// Only interactive runs install one; without it (or with a nil approver, as
// during Run preparation), needs_approval fails closed without prompting.
type mcpConnectionApprover func(context.Context, config.MCPServer) (bool, error)

func withMCPConnectionApprover(ctx context.Context, approve mcpConnectionApprover) context.Context {
	return context.WithValue(ctx, mcpConnectionApproverKey{}, approve)
}

// approveIfNeeded runs before an operation acquires the service. Prompts are
// serialized so concurrent discovery asks once per service, and the answer is
// remembered for this exact fingerprint for the owner's lifetime. No owner or
// Guard lock is held while the user decides.
func (o *mcpOwner) approveIfNeeded(ctx context.Context, s *mcpOwnedService) error {
	approve, _ := ctx.Value(mcpConnectionApproverKey{}).(mcpConnectionApprover)
	if approve == nil || s == nil {
		return nil
	}
	o.approvalMu.Lock()
	defer o.approvalMu.Unlock()
	o.mu.Lock()
	pending := o.policyLocked(s) != nil && s.status.State == "needs_approval"
	server := s.server
	o.mu.Unlock()
	if !pending {
		return nil
	}
	allowed, err := approve(ctx, server)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.server.Fingerprint != server.Fingerprint {
		return fmt.Errorf("MCP connection changed during approval")
	}
	decisions := o.declined
	if allowed {
		decisions = o.approved
	}
	decisions[server.Key] = server.Fingerprint
	_ = o.policyLocked(s) // publish the resulting status
	return nil
}

// approveMCPConnection shows the exact launch parameters in the guard panel.
// Any reply other than an offered allow option, including cancellation, denies.
func (s *interactiveSession) approveMCPConnection(paths config.Paths) mcpConnectionApprover {
	return func(ctx context.Context, server config.MCPServer) (bool, error) {
		if s == nil || s.guardRequests == nil {
			return false, fmt.Errorf("MCP connection approval needs an interactive frontend")
		}
		options := []interaction.GuardOption{
			{ID: guardOptionAllowMCPConnection, Label: "Allow this connection while AICE is running"},
			{ID: guardOptionAllowMCPConnectionSaved, Label: "Always allow this exact connection", Detail: "Saved for this fingerprint; a changed command, endpoint or credential asks again"},
			{ID: guardOptionDeny, Label: "Deny", Deny: true},
		}
		promptCtx, closePrompt := context.WithCancel(ctx)
		defer closePrompt()
		reply := make(chan interaction.GuardReply, 1)
		title := "Connect to this MCP service?"
		if server.Settings.Transport == "stdio" {
			title = "Start this MCP service?"
		}
		request := interaction.GuardRequest{
			ID:       "mcp-connection:" + server.Key,
			Title:    title,
			ToolName: "MCP connection",
			Reason:   fmt.Sprintf("Start MCP service %s (%s). Tool calls still ask separately.", server.Key, server.Source.Kind+":"+server.Source.Location),
			RuleID:   guardRuleMCPConnection,
			Command:  mcpConnectionSummary(server),
			Options:  options,
			Reply:    reply,
			Done:     promptCtx.Done(),
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case s.guardRequests <- request:
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case got := <-reply:
			if err := ctx.Err(); err != nil {
				return false, err
			}
			switch got.OptionID {
			case guardOptionAllowMCPConnection:
				return true, nil
			case guardOptionAllowMCPConnectionSaved:
				if err := config.SaveMCPConnectionDecision(ctx, paths, server, config.MCPConnectionAllow); err != nil {
					return false, fmt.Errorf("MCP connection approval could not be saved: %w", err)
				}
				return true, nil
			}
			return false, nil
		}
	}
}

// mcpConnectionSummary is what the user approves: the resolved executable,
// arguments and directory, or the endpoint. Credential values never appear.
func mcpConnectionSummary(server config.MCPServer) string {
	summary := server.Settings.URL
	if server.Settings.Transport == "stdio" {
		executable := server.Executable
		if executable == "" {
			executable = server.Settings.Command
		}
		summary = strings.Join(append([]string{executable}, server.Settings.Args...), " ") + "  (in " + server.WorkingDir + ")"
	}
	if len(summary) > mcpConnectionApprovalCommandMaxSize {
		summary = summary[:mcpConnectionApprovalCommandMaxSize] + "…"
	}
	return summary
}
