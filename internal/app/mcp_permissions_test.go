package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestMCPPermissionCLIAndPrintAcrossSessions(t *testing.T) {
	paths := trustTestPaths(t)
	endpoint, opens, calls, closes := mcpStartupServer(t)
	if _, err := managementCommand(t, paths, `{"transport":"http","url":"`+endpoint+`"}`, "add", "service0"); err != nil {
		t.Fatal(err)
	}
	status, _ := managementCommand(t, paths, "", "status", "user:service0")
	binding := status.Services[0]
	if _, err := managementCommand(t, paths, "", "permissions", binding.Key); err == nil || opens.Load() != 0 {
		t.Fatal("unapproved permission discovery connected")
	}
	if _, err := managementCommand(t, paths, "", "approve", binding.Key, "--fingerprint", binding.Fingerprint); err != nil {
		t.Fatal(err)
	}
	inspected, err := managementCommand(t, paths, "", "permissions", binding.Key)
	if err != nil || len(inspected.Permissions) != 1 || calls.Load() != 0 || opens.Load() != 1 || closes.Load() != 1 {
		t.Fatal("inspection executed tool or leaked connection", inspected, err)
	}
	p := inspected.Permissions[0]
	save := func(decision, schema string) {
		t.Helper()
		before := opens.Load()
		result, err := managementCommand(t, paths, "", "permission", binding.Key, "--fingerprint", binding.Fingerprint, "--scope", binding.PermissionScope, "--tool", p.Tool, "--schema-fingerprint", schema, "--decision", decision)
		if err != nil || !result.Committed || opens.Load() != before {
			t.Fatal("permission save failed or connected", result, err)
		}
	}
	print := func(wantRemote, wantCall, yolo bool) {
		t.Helper()
		model := &mcpStartupModel{t: t, search: true, wantRemote: wantRemote}
		command, err := newTestCommand(t, dependencies{
			loadConfig: func(options config.LoadOptions) (config.Config, error) {
				c, err := config.LoadFiles(paths, options)
				c.DeepSeekAPIKey = "offline-fixture"
				return c, err
			},
			newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "session.jsonl")
		args := []string{"--workspace", t.TempDir(), "--no-dep-install", "--no-update-check", "--session", path, "--print", "read fixture"}
		if yolo {
			args = append(args, "--yolo")
		}
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		before := calls.Load()
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if (calls.Load() == before+1) != wantCall || calls.Load() > before+1 {
			t.Fatal("unexpected remote calls", calls.Load()-before)
		}
		store, err := session.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		snapshot, err := store.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range snapshot.Messages {
			if result, ok := entry.Message.(llm.ToolResultMessage); ok && result.ToolCallID == "execute" {
				if result.Details == nil || (result.Details.State == llm.ExecutionReturned) != wantCall {
					t.Fatal("incorrect persisted dispatch state")
				}
			}
		}
	}
	save("allow", p.SchemaFingerprint)
	print(true, true, false)
	print(true, true, false) // fresh application and Session, no restored Session grant
	save("allow", strings.Repeat("b", 64))
	print(true, false, false) // reviewed schema differs from actual remote schema
	save("deny", strings.Repeat("b", 64))
	print(false, false, true) // deny still applies to the current schema under yolo
	save("ask", p.SchemaFingerprint)
	print(true, false, false)
	save("allow", p.SchemaFingerprint)
	if _, err := managementCommand(t, paths, "", "forget", binding.Key, "--fingerprint", binding.Fingerprint); err != nil {
		t.Fatal(err)
	}
	print(false, false, false) // permanent operation rule never authorizes a connection
	if _, err := managementCommand(t, paths, "", "remove", binding.Key); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(data), p.SchemaFingerprint) {
		t.Fatal("remove retained durable operation rule")
	}
}

func TestMCPPermissionSettingsInvalidatesOldCatalog(t *testing.T) {
	endpoint, _, calls, _ := mcpStartupServer(t)
	runMCPSettingsFixture(t, `{"transport":"http","url":"`+endpoint+`"}`, func(ctx context.Context, s *interactiveSession) {
		act := func(action string, values ...string) (interaction.SettingsActionResult, error) {
			revision, _ := s.settingsStatus()
			return s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: action + " user:docs", Auth: mcpTestInput(t, values...)})
		}
		if _, err := act("approve", "confirm"); err != nil {
			t.Fatal(err)
		}
		_, old, err := s.mcp.bindRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		found, err := old.catalog.Search(ctx, tool.ToolSearchRequest{Service: "user:docs", Limit: 1})
		if err != nil || len(found.Selected) != 1 {
			t.Fatal(err)
		}
		result, err := act("permission", "allow", "0", "save")
		if err != nil || !result.Applied || !result.Committed || old.catalog.Check(ctx, found.Selected[0]) == nil {
			t.Fatal("saved rule did not replace runtime", result, err)
		}
		_, fresh, err := s.mcp.bindRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		selected, err := fresh.catalog.Search(ctx, tool.ToolSearchRequest{Service: "user:docs", Limit: 1})
		if err != nil || len(selected.Selected) != 1 {
			t.Fatal(err)
		}
		resolved, _ := fresh.catalog.Resolve(ctx, []string{selected.Selected[0].ID})
		binding, scope, _, _ := fresh.catalog.MCPBinding(ctx, resolved[0].Tool.Definition().Name)
		decision, permit, err := s.guard.CheckMCP(ctx, "read", binding, scope)
		if err != nil || decision.Decision != guard.DecisionAllow {
			t.Fatal("saved rule not applied", err)
		}
		s.guard.ResetSessionGrants()
		decision, _, _ = s.guard.CheckMCP(ctx, "read", binding, scope)
		if decision.Decision != guard.DecisionAllow {
			t.Fatal("user rule lost on Session reset")
		}
		if _, err := act("permission", "ask", "0", "save"); err != nil {
			t.Fatal(err)
		}
		if permit.Validate(ctx) == nil || fresh.catalog.Check(ctx, selected.Selected[0]) == nil {
			t.Fatal("removed rule retained old permit/catalog")
		}
		oldOwner := s.mcp
		cancelled, cancelErr := act("permission", "allow", "0", "cancel")
		if cancelErr == nil || cancelled.Committed || s.mcp != oldOwner || len(s.configuration.MCP.Permissions("user:docs")) != 0 {
			t.Fatal("canceled permission changed authority")
		}
		if calls.Load() != 0 || s.conversation.store != nil {
			t.Fatal("management executed tool or wrote Session")
		}
	})
}

func TestMCPPermissionRejectsChangedScopeAndFingerprint(t *testing.T) {
	c := ownerTestConfig(t, 1)
	server := c.MCP.Servers["user:service0"]
	p := &interaction.MCPPermission{Tool: "read", SchemaFingerprint: strings.Repeat("a", 64), Decision: "allow"}
	for _, change := range []string{"scope", "fingerprint", "decision", "operation"} {
		t.Run(change, func(t *testing.T) {
			rule := *p
			request := interaction.MCPRequest{Action: "permission", Key: server.Key, Fingerprint: server.Fingerprint, PermissionScope: c.MCP.PermissionScope(server.Key), Permission: &rule}
			switch change {
			case "scope":
				request.PermissionScope = "old"
			case "fingerprint":
				request.Fingerprint = "old"
			case "decision":
				rule.Decision = "always"
			case "operation":
				rule.Operation = "tools/call"
			}
			_, result, err := executeMCPManagement(t.Context(), c, nil, request, nil)
			if err == nil || result.Committed {
				t.Fatal("invalid permission saved")
			}
		})
	}
	// Resource-read and ordinary tool names cannot share a rule.
	resource := config.MCPPermission{Operation: llm.OperationResourceRead, Tool: llm.OperationResourceRead, SchemaFingerprint: mcpDigest(mcpResourceReadSchema), Decision: "allow"}
	next, err := c.WithMCPPermission(server.Key, server.Fingerprint, resource)
	if err != nil {
		t.Fatal(err)
	}
	ordinarySchema := mcpDigest([]json.RawMessage{json.RawMessage(mcpResourceReadSchema), nil})
	if next.MCP.PermissionDecision(server.Key, "", llm.OperationResourceRead, ordinarySchema) != "ask" {
		t.Fatal("resource permission crossed operation domain")
	}
}
