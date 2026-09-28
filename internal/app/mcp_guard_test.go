package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

type mcpGuardTestBindings struct {
	binding llm.ToolBinding
	scope   string
	closed  bool
}

func (b *mcpGuardTestBindings) MCPBinding(ctx context.Context, name string) (llm.ToolBinding, string, bool, error) {
	if b.closed {
		return llm.ToolBinding{}, "", false, errors.New("closed")
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolBinding{}, "", false, err
	}
	return b.binding, b.scope, name == "mcp_docs_read", nil
}

func mcpGuardTestFixture(t *testing.T, yolo bool) (*interactiveSession, *guardAdapter, guard.MCPService) {
	t.Helper()
	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	service := guard.MCPService{
		Source: "user:/settings.json", ServiceID: "docs", ConnectionFingerprint: "connection", PermissionScope: "tools",
		Enabled: true, Tools: []guard.MCPToolPolicy{{Name: "read", SchemaFingerprint: "schema", Allowed: true}, {Name: "other", SchemaFingerprint: "other-schema", Allowed: true}},
	}
	if err := session.guard.SetMCPService(service); err != nil {
		t.Fatal(err)
	}
	bindings := &mcpGuardTestBindings{
		binding: llm.ToolBinding{Source: service.Source, ServiceID: service.ServiceID, ConnectionFingerprint: service.ConnectionFingerprint, ToolName: "read", SchemaFingerprint: "schema"},
		scope:   service.PermissionScope,
	}
	return session, &guardAdapter{inner: session.guard, mcp: bindings, yolo: yolo}, service
}

func runMCPGuardLoop(t *testing.T, adapter *guardAdapter, ask agent.GuardAskHandler) int {
	t.Helper()
	call := llm.ToolCall{ID: "mcp-call", Name: "mcp_docs_read", Arguments: []byte(`{"source":"fake-allow","readOnlyHint":true}`)}
	model := &toolLoopModel{firstCall: &call}
	executions := 0
	tool := newAppTestTool(call.Name, func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		executions++
		return llm.ToolResult{Content: []llm.ContentPart{llm.NewTextContent("test result").Part()}}, nil
	})
	loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(adapter), agent.WithGuardAskHandler(ask))
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := llm.NewUserMessage(llm.NewTextContent("test MCP execution permission").Part())
	result, err := loop.Run(t.Context(), agent.RunInput{Model: deepseek.DefaultModel(), Prompt: prompt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, message := range result.Messages() {
		if paired, ok := message.(llm.ToolResultMessage); ok {
			results++
			if paired.ToolCallID != call.ID || paired.IsError != (executions == 0) {
				t.Fatalf("unpaired or misleading result: %+v; executed %d", paired, executions)
			}
		}
	}
	if results != 1 {
		t.Fatalf("got %d tool results", results)
	}
	return executions
}

func answerMCPGuard(t *testing.T, session *interactiveSession, option string, beforeReply func()) agent.GuardAskHandler {
	t.Helper()
	return func(ctx context.Context, call llm.ToolCall, approval agent.GuardApproval) (agent.GuardAskReply, error) {
		type response struct {
			reply agent.GuardAskReply
			err   error
		}
		done := make(chan response, 1)
		go func() {
			reply, err := session.handleGuardAsk(ctx, call, approval)
			done <- response{reply, err}
		}()
		select {
		case request := <-session.guardRequests:
			if beforeReply != nil {
				beforeReply()
			}
			request.Reply <- interaction.GuardReply{OptionID: option}
		case <-ctx.Done():
		}
		got := <-done
		return got.reply, got.err
	}
}

func TestMCPGuardLoopApprovalScopes(t *testing.T) {
	t.Parallel()
	for _, option := range []string{guardOptionAllowOnce, guardOptionAllowMCPTool, guardOptionAllowMCPService, guardOptionDeny, guardOptionAllowRunTool} {
		t.Run(option, func(t *testing.T) {
			session, adapter, service := mcpGuardTestFixture(t, false)
			// The old unknown-tool grant must not authorize a mapped MCP tool.
			session.guard.AllowToolSession("mcp_docs_read")
			if got := runMCPGuardLoop(t, adapter, nil); got != 0 {
				t.Fatal("noninteractive ask dispatched")
			}
			got := runMCPGuardLoop(t, adapter, answerMCPGuard(t, session, option, nil))
			want := option != guardOptionDeny && option != guardOptionAllowRunTool
			if (got == 1) != want {
				t.Fatalf("execution = %d", got)
			}
			got = runMCPGuardLoop(t, adapter, nil)
			if (got == 1) != (option == guardOptionAllowMCPTool || option == guardOptionAllowMCPService) {
				t.Fatalf("next run execution = %d", got)
			}
			binding := adapter.mcp.(*mcpGuardTestBindings).binding
			binding.ToolName, binding.SchemaFingerprint = "other", "other-schema"
			other, _, err := session.guard.CheckMCP(t.Context(), "other-model-name", binding, service.PermissionScope)
			if err != nil || (other.Decision == guard.DecisionAllow) != (option == guardOptionAllowMCPService) {
				t.Fatalf("other tool grant: %+v, %v", other, err)
			}
			session.guard.ResetSessionGrants()
			if runMCPGuardLoop(t, adapter, nil) != 0 {
				t.Fatal("Session reset retained MCP grant")
			}
		})
	}
}

func TestMCPGuardLoopYoloPreservesDenials(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"none", "disabled", "filtered", "revoked", "removed", "schema", "scope", "closed-run"} {
		t.Run(failure, func(t *testing.T) {
			session, adapter, service := mcpGuardTestFixture(t, true)
			switch failure {
			case "disabled":
				service.Enabled = false
			case "filtered":
				service.Tools[0].Allowed = false
			case "revoked":
				session.guard.RevokeMCPService(service.Source, service.ServiceID)
			case "schema":
				service.Tools[0].SchemaFingerprint = "new-schema"
			case "scope":
				service.PermissionScope = "new-scope"
			case "closed-run":
				adapter.mcp.(*mcpGuardTestBindings).closed = true
			}
			if err := session.guard.SetMCPService(service); err != nil {
				t.Fatal(err)
			}
			if failure == "removed" {
				session.guard.RemoveMCPService(service.Source, service.ServiceID)
			}
			if got := runMCPGuardLoop(t, adapter, nil); (got == 1) != (failure == "none") {
				t.Fatalf("executions = %d", got)
			}
			// Yolo authorizes this invocation without writing a Session grant.
			adapter.yolo = false
			if runMCPGuardLoop(t, adapter, nil) != 0 {
				t.Fatal("yolo persisted an implicit Session grant")
			}
		})
	}
}

func TestMCPGuardRevalidatesAfterInteractiveApproval(t *testing.T) {
	t.Parallel()
	for _, option := range []string{guardOptionAllowOnce, guardOptionAllowMCPTool, guardOptionAllowMCPService} {
		for _, change := range []string{"revoke", "schema", "remove-and-restore", "new-session", "run-binding"} {
			t.Run(option+"/"+change, func(t *testing.T) {
				session, adapter, service := mcpGuardTestFixture(t, false)
				ask := answerMCPGuard(t, session, option, func() {
					switch change {
					case "revoke":
						session.guard.RevokeMCPService(service.Source, service.ServiceID)
					case "schema":
						service.Tools[0].SchemaFingerprint = "new-schema"
						if err := session.guard.SetMCPService(service); err != nil {
							t.Fatal(err)
						}
					case "remove-and-restore":
						session.guard.RemoveMCPService(service.Source, service.ServiceID)
						if err := session.guard.SetMCPService(service); err != nil {
							t.Fatal(err)
						}
					case "new-session":
						session.guard.ResetSessionGrants()
					case "run-binding":
						adapter.mcp.(*mcpGuardTestBindings).closed = true
					}
				})
				if got := runMCPGuardLoop(t, adapter, ask); got != 0 {
					t.Fatal("tool executed after approval became stale")
				}
			})
		}
	}
}
