package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestApplicationInteractiveFirstLoginKeepsWorkspaceGuard(t *testing.T) {
	t.Parallel()

	const secret = "guard-secret"
	workspace := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workspace, ".env"),
		[]byte(secret),
		0o600,
	); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	call := llm.ToolCall{
		ID:        "read-1",
		Name:      "read",
		Arguments: []byte(`{"path":".env"}`),
	}
	model := &toolLoopModel{firstCall: &call}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		saveSetting: func(config.Setting, string) error { return nil },
		saveAPIKey: func(string, string) (string, error) {
			return "/global/auth.json", nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			_ tui.Options,
		) error {
			commandRunner := runner.(tui.SlashCommandRunner)
			if _, err := commandRunner.RunSlashCommand(
				ctx,
				tui.SlashCommandRequest{
					Name:   "login",
					Secret: "test-key",
				},
			); err != nil {
				return err
			}
			return runInteractive(ctx, runner, "inspect", nil)
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want tool call and completion", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) == 0 {
		t.Fatal("second model request has no messages")
	}
	result, ok := messages[len(messages)-1].(llm.ToolResultMessage)
	if !ok {
		t.Fatalf("last message = %T, want ToolResultMessage", messages[len(messages)-1])
	}
	if !result.IsError {
		t.Fatalf(".env tool result = %#v, want guard denial", result)
	}
	for _, part := range result.Content {
		if strings.Contains(part.Text, secret) {
			t.Fatalf(".env contents reached model context: %q", part.Text)
		}
	}
}

func TestMapGuardResultPassesPattern(t *testing.T) {
	t.Parallel()

	got := mapGuardResult(guard.Result{
		Decision: guard.DecisionAsk,
		Reason:   "dangerous",
		RuleID:   guardRuleDangerous,
		Pattern:  "rm -rf x",
		Action: guard.Action{
			Kind:     "command",
			Command:  "rm -rf x",
			ToolName: "bash",
		},
	})
	if got.Pattern != "rm -rf x" {
		t.Fatalf("mapGuardResult() Pattern = %q, want %q", got.Pattern, "rm -rf x")
	}
	if got.Decision != agent.GuardAsk {
		t.Fatalf("mapGuardResult() Decision = %q, want ask", got.Decision)
	}
}

func TestGuardAskOptions(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	gate, err := guard.New(workspace, guard.Config{})
	if err != nil {
		t.Fatalf("guard.New() error = %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "nested", "secret.txt")
	home, homeErr := os.UserHomeDir()
	homeFile := ""
	if homeErr == nil && home != "" {
		homeFile = filepath.Join(home, "aice-guard-ask-home-file.txt")
	}

	tests := []struct {
		name     string
		skip     bool
		result   agent.GuardResult
		toolName string
		wantIDs  []string
	}{
		{
			name: "path access asks for file and directory",
			result: agent.GuardResult{
				RuleID: guardRulePathAccessAsk,
				Action: agent.GuardAction{Kind: "file", Path: outsideFile, ToolName: "read"},
			},
			toolName: "read",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionAllowRunFile,
				guardOptionAllowRunDir,
				guardOptionDeny,
			},
		},
		{
			name: "path access omits directory at home",
			skip: homeFile == "",
			result: agent.GuardResult{
				RuleID: guardRulePathAccessAsk,
				Action: agent.GuardAction{Kind: "file", Path: homeFile, ToolName: "read"},
			},
			toolName: "read",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionAllowRunFile,
				guardOptionDeny,
			},
		},
		{
			name: "dangerous command with prefix",
			result: agent.GuardResult{
				RuleID: guardRuleDangerous,
				Action: agent.GuardAction{
					Kind:     "command",
					Command:  "git push origin main",
					ToolName: "bash",
				},
			},
			toolName: "bash",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionAllowRunCommand,
				guardOptionAllowRunPrefix,
				guardOptionDeny,
			},
		},
		{
			name: "dangerous rm has no prefix",
			result: agent.GuardResult{
				RuleID: guardRuleDangerous,
				Action: agent.GuardAction{
					Kind:     "command",
					Command:  "rm -rf x",
					ToolName: "bash",
				},
			},
			toolName: "bash",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionAllowRunCommand,
				guardOptionDeny,
			},
		},
		{
			name: "unknown tool",
			result: agent.GuardResult{
				RuleID: guardRuleUnknownTool,
				Action: agent.GuardAction{ToolName: "web_search"},
			},
			toolName: "web_search",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionAllowRunTool,
				guardOptionDeny,
			},
		},
		{
			name: "unknown rule id",
			result: agent.GuardResult{
				RuleID: "policy.secret-files",
				Action: agent.GuardAction{Kind: "file", Path: outsideFile, ToolName: "read"},
			},
			toolName: "read",
			wantIDs: []string{
				guardOptionAllowOnce,
				guardOptionDeny,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.skip {
				t.Skip("user home is required")
			}

			options := guardAskOptions(gate, test.toolName, test.result)
			got := guardOptionIDs(options)
			if !slices.Equal(got, test.wantIDs) {
				t.Fatalf("option IDs = %v, want %v", got, test.wantIDs)
			}
			for _, option := range options {
				wantDeny := option.ID == guardOptionDeny
				if option.Deny != wantDeny {
					t.Fatalf("option %q Deny = %v, want %v", option.ID, option.Deny, wantDeny)
				}
			}
		})
	}
}

func TestGuardAskOptionsPathAccessLabels(t *testing.T) {
	t.Parallel()

	gate, err := guard.New(t.TempDir(), guard.Config{})
	if err != nil {
		t.Fatalf("guard.New() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "nested", "secret.txt")
	abs := gate.ResolveAbsolute(path, "read")
	options := guardAskOptions(gate, "read", agent.GuardResult{
		RuleID: guardRulePathAccessAsk,
		Action: agent.GuardAction{Kind: "file", Path: path, ToolName: "read"},
	})

	file := findGuardOption(t, options, guardOptionAllowRunFile)
	if file.Label != "Allow this file for this run" {
		t.Fatalf("allow-run-file Label = %q", file.Label)
	}
	if file.Detail != shortenHomePath(abs) {
		t.Fatalf("allow-run-file Detail = %q, want %q", file.Detail, shortenHomePath(abs))
	}

	dir := findGuardOption(t, options, guardOptionAllowRunDir)
	wantDir := "Allow directory " + shortenHomePath(filepath.Dir(abs)) + "/ for this run"
	if dir.Label != wantDir {
		t.Fatalf("allow-run-dir Label = %q, want %q", dir.Label, wantDir)
	}
}

func TestHandleGuardAskHighlightFromPattern(t *testing.T) {
	t.Parallel()

	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	call := llm.ToolCall{ID: "call-1", Name: "bash", Arguments: mustCommandArgs(t, "rm -rf x")}
	_, request := handleGuardAskWithReply(t, session, call, agent.GuardResult{
		Decision: agent.GuardAsk,
		RuleID:   guardRuleDangerous,
		Pattern:  "rm -rf x",
		Action: agent.GuardAction{
			Kind:     "command",
			Command:  "rm -rf x",
			ToolName: "bash",
		},
	}, interaction.GuardReply{OptionID: guardOptionAllowOnce})
	if request.Highlight != "rm -rf x" {
		t.Fatalf("Highlight = %q, want %q", request.Highlight, "rm -rf x")
	}
}

func TestHandleGuardAskAllowRunFileGrantsPathNotParent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outsideDir := filepath.Join(t.TempDir(), "out")
	granted := filepath.Join(outsideDir, "granted.txt")
	sibling := filepath.Join(outsideDir, "sibling.txt")
	session := newGuardAskSession(t, workspace, guard.Config{})

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: mustFilePathArgs(t, granted),
	}
	reply, _ := handleGuardAskWithReply(t, session, call, pathAccessAskResult(granted), interaction.GuardReply{
		OptionID: guardOptionAllowRunFile,
	})
	if reply.Decision != agent.GuardAllow {
		t.Fatalf("handleGuardAsk() decision = %q, want allow", reply.Decision)
	}

	assertPathDecision(t, session.guard, granted, guard.DecisionAllow)
	assertPathDecision(t, session.guard, sibling, guard.DecisionAsk)
}

func TestHandleGuardAskAllowRunDirGrantsSibling(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outsideDir := filepath.Join(t.TempDir(), "out")
	granted := filepath.Join(outsideDir, "granted.txt")
	sibling := filepath.Join(outsideDir, "sibling.txt")
	other := filepath.Join(t.TempDir(), "other", "other.txt")
	session := newGuardAskSession(t, workspace, guard.Config{})

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: mustFilePathArgs(t, granted),
	}
	reply, _ := handleGuardAskWithReply(t, session, call, pathAccessAskResult(granted), interaction.GuardReply{
		OptionID: guardOptionAllowRunDir,
	})
	if reply.Decision != agent.GuardAllow {
		t.Fatalf("handleGuardAsk() decision = %q, want allow", reply.Decision)
	}

	assertPathDecision(t, session.guard, granted, guard.DecisionAllow)
	assertPathDecision(t, session.guard, sibling, guard.DecisionAllow)
	assertPathDecision(t, session.guard, other, guard.DecisionAsk)
}

func TestHandleGuardAskAllowRunToolGrantsUnknownTool(t *testing.T) {
	t.Parallel()

	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "web_search",
		Arguments: json.RawMessage(`{}`),
	}
	before, err := session.guard.Check(t.Context(), call)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if before.Decision != guard.DecisionAsk || before.RuleID != guardRuleUnknownTool {
		t.Fatalf("Check() before grant = %q %q, want ask unknownTool", before.Decision, before.RuleID)
	}

	reply, _ := handleGuardAskWithReply(t, session, call, mapGuardResult(before), interaction.GuardReply{
		OptionID: guardOptionAllowRunTool,
	})
	if reply.Decision != agent.GuardAllow {
		t.Fatalf("handleGuardAsk() decision = %q, want allow", reply.Decision)
	}

	after, err := session.guard.Check(t.Context(), call)
	if err != nil {
		t.Fatalf("Check() after grant error = %v", err)
	}
	if after.Decision != guard.DecisionAllow {
		t.Fatalf("granted tool decision = %q, want allow", after.Decision)
	}

	other, err := session.guard.Check(t.Context(), llm.ToolCall{
		ID:        "call-2",
		Name:      "custom",
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Check(other) error = %v", err)
	}
	if other.Decision != guard.DecisionAsk {
		t.Fatalf("ungranted tool decision = %q, want ask", other.Decision)
	}
}

func TestHandleGuardAskAllowRunPrefixBypassesDangerous(t *testing.T) {
	t.Parallel()

	cfg := guard.Config{}
	cfg.PermissionGate.CustomPatterns = []guard.PatternConfig{
		{Pattern: "push", Description: "push"},
	}
	session := newGuardAskSession(t, t.TempDir(), cfg)
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: mustCommandArgs(t, "git push origin main"),
	}
	before, err := session.guard.Check(t.Context(), call)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if before.Decision != guard.DecisionAsk || before.RuleID != guardRuleDangerous {
		t.Fatalf("Check() before grant = %q %q, want ask dangerous", before.Decision, before.RuleID)
	}

	reply, request := handleGuardAskWithReply(t, session, call, mapGuardResult(before), interaction.GuardReply{
		OptionID: guardOptionAllowRunPrefix,
	})
	if !guardOptionOffered(request.Options, guardOptionAllowRunPrefix) {
		t.Fatalf("options = %v, want allow-run-prefix", guardOptionIDs(request.Options))
	}
	if reply.Decision != agent.GuardAllow {
		t.Fatalf("handleGuardAsk() decision = %q, want allow", reply.Decision)
	}

	after, err := session.guard.Check(t.Context(), call)
	if err != nil {
		t.Fatalf("Check() after grant error = %v", err)
	}
	if after.Decision != guard.DecisionAllow {
		t.Fatalf("prefixed command decision = %q, want allow", after.Decision)
	}

	related, err := session.guard.Check(t.Context(), llm.ToolCall{
		ID:        "call-2",
		Name:      "bash",
		Arguments: mustCommandArgs(t, "git push origin other"),
	})
	if err != nil {
		t.Fatalf("Check(related) error = %v", err)
	}
	if related.Decision != guard.DecisionAllow {
		t.Fatalf("related prefix command decision = %q, want allow", related.Decision)
	}
}

func TestHandleGuardAskRejectsUnofferedOption(t *testing.T) {
	t.Parallel()

	t.Run("forged directory grant at home", func(t *testing.T) {
		t.Parallel()
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			t.Skip("user home is required")
		}
		homeFile := filepath.Join(home, "aice-guard-ask-home-file.txt")
		session := newGuardAskSession(t, t.TempDir(), guard.Config{})
		call := llm.ToolCall{
			ID:        "call-1",
			Name:      "read",
			Arguments: mustFilePathArgs(t, homeFile),
		}
		reply, request := handleGuardAskWithReply(t, session, call, pathAccessAskResult(homeFile), interaction.GuardReply{
			OptionID: guardOptionAllowRunDir,
		})
		if guardOptionOffered(request.Options, guardOptionAllowRunDir) {
			t.Fatal("home file unexpectedly offered allow-run-dir")
		}
		if reply.Decision != agent.GuardDeny {
			t.Fatalf("handleGuardAsk() decision = %q, want deny", reply.Decision)
		}
	})

	t.Run("forged tool grant on unknown rule", func(t *testing.T) {
		t.Parallel()
		session := newGuardAskSession(t, t.TempDir(), guard.Config{})
		call := llm.ToolCall{
			ID:        "call-1",
			Name:      "web_search",
			Arguments: json.RawMessage(`{}`),
		}
		reply, request := handleGuardAskWithReply(t, session, call, agent.GuardResult{
			Decision: agent.GuardAsk,
			RuleID:   "policy.secret-files",
			Action:   agent.GuardAction{ToolName: "web_search"},
		}, interaction.GuardReply{OptionID: guardOptionAllowRunTool})
		if guardOptionOffered(request.Options, guardOptionAllowRunTool) {
			t.Fatal("unknown rule unexpectedly offered allow-run-tool")
		}
		if reply.Decision != agent.GuardDeny {
			t.Fatalf("handleGuardAsk() decision = %q, want deny", reply.Decision)
		}
		after, err := session.guard.Check(t.Context(), call)
		if err != nil {
			t.Fatalf("Check() error = %v", err)
		}
		if after.Decision != guard.DecisionAsk {
			t.Fatalf("ungranted tool decision = %q, want ask", after.Decision)
		}
	})
}

func TestHandleGuardAskDenyFeedback(t *testing.T) {
	t.Parallel()

	session := newGuardAskSession(t, t.TempDir(), guard.Config{})
	path := filepath.Join(t.TempDir(), "nested", "secret.txt")
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: mustFilePathArgs(t, path),
	}
	const feedback = "do not read that file"
	reply, _ := handleGuardAskWithReply(t, session, call, pathAccessAskResult(path), interaction.GuardReply{
		OptionID: guardOptionDeny,
		Feedback: feedback,
	})
	if reply.Decision != agent.GuardDeny {
		t.Fatalf("handleGuardAsk() decision = %q, want deny", reply.Decision)
	}
	if reply.Feedback != feedback {
		t.Fatalf("handleGuardAsk() Feedback = %q, want %q", reply.Feedback, feedback)
	}
}

func newGuardAskSession(t *testing.T, workspace string, cfg guard.Config) *interactiveSession {
	t.Helper()
	gate, err := guard.New(workspace, cfg)
	if err != nil {
		t.Fatalf("guard.New() error = %v", err)
	}
	return &interactiveSession{
		guard:         gate,
		guardRequests: make(chan interaction.GuardRequest, 1),
	}
}

func handleGuardAskWithReply(
	t *testing.T,
	session *interactiveSession,
	call llm.ToolCall,
	result agent.GuardResult,
	reply interaction.GuardReply,
) (agent.GuardAskReply, interaction.GuardRequest) {
	t.Helper()
	requests := make(chan interaction.GuardRequest, 1)
	go func() {
		request := <-session.guardRequests
		requests <- request
		request.Reply <- reply
	}()
	got, err := session.handleGuardAsk(t.Context(), call, result)
	if err != nil {
		t.Fatalf("handleGuardAsk() error = %v", err)
	}
	return got, <-requests
}

func pathAccessAskResult(path string) agent.GuardResult {
	return agent.GuardResult{
		Decision: agent.GuardAsk,
		RuleID:   guardRulePathAccessAsk,
		Action: agent.GuardAction{
			Kind:     "file",
			Path:     path,
			ToolName: "read",
		},
	}
}

func assertPathDecision(t *testing.T, gate *guard.Guard, path string, want guard.Decision) {
	t.Helper()
	result, err := gate.Check(t.Context(), llm.ToolCall{
		ID:        "check",
		Name:      "read",
		Arguments: mustFilePathArgs(t, path),
	})
	if err != nil {
		t.Fatalf("Check(%s) error = %v", path, err)
	}
	if result.Decision != want {
		t.Fatalf("Check(%s) decision = %q, want %q", path, result.Decision, want)
	}
}

func guardOptionIDs(options []interaction.GuardOption) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	return ids
}

func findGuardOption(t *testing.T, options []interaction.GuardOption, id string) interaction.GuardOption {
	t.Helper()
	for _, option := range options {
		if option.ID == id {
			return option
		}
	}
	t.Fatalf("option %q not found in %v", id, guardOptionIDs(options))
	return interaction.GuardOption{}
}

func mustFilePathArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"file_path": path})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}

func mustCommandArgs(t *testing.T, command string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
