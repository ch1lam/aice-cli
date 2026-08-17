package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestSerializeCompactionMessagesUsesBoundedTranscriptText(t *testing.T) {
	t.Parallel()

	user, err := llm.NewUserMessage(llm.NewTextContent("preserve this request").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	assistant := llm.NewAssistantMessage(llm.Model{
		ID:       "test-model",
		API:      "test-api",
		Provider: "test-provider",
	})
	assistant.Content = []llm.ContentPart{{
		Type: llm.ContentTypeToolCall,
		ToolCall: &llm.ToolCall{
			ID:        "call-1",
			Name:      "read",
			Arguments: []byte(`{"path":"a.go"}`),
		},
	}}
	toolResult, err := llm.NewToolResultMessage(llm.ToolResult{
		CallID: "call-1",
		Name:   "read",
		Content: []llm.ContentPart{
			llm.NewTextContent(strings.Repeat("x", compactionToolResultMaxChars+100)).Part(),
		},
	})
	if err != nil {
		t.Fatalf("NewToolResultMessage() error = %v", err)
	}

	serialized, err := serializeCompactionMessages([]llm.AgentMessage{
		user,
		assistant,
		toolResult,
		llm.CompactionSummaryMessage{
			Role:         llm.RoleCompactionSummary,
			Summary:      "previous checkpoint",
			TokensBefore: 100,
			Timestamp:    1,
		},
	})
	if err != nil {
		t.Fatalf("serializeCompactionMessages() error = %v", err)
	}
	if !strings.Contains(serialized, `[User]
preserve this request`) {
		t.Errorf("serialized user message = %q", serialized)
	}
	if !strings.Contains(serialized, `[Tool call id="call-1" name="read" arguments={"path":"a.go"}]`) {
		t.Errorf("serialized tool call = %q", serialized)
	}
	if strings.Contains(serialized, strings.Repeat("x", compactionToolResultMaxChars+100)) {
		t.Errorf("serialized tool result was not bounded")
	}
	if !strings.Contains(serialized, "Tool result text truncated") {
		t.Errorf("serialized tool result has no truncation marker")
	}
	if !strings.Contains(serialized, "previous checkpoint") {
		t.Errorf("serialized previous summary = %q", serialized)
	}

	truncated := truncateCompactionTranscript(strings.Repeat("x", 20_000), 100)
	if got := llm.EstimateTextTokens(truncated); got > 100 {
		t.Errorf("truncated transcript estimate = %d, want at most 100", got)
	}
	if !strings.Contains(truncated, "older transcript omitted") {
		t.Errorf("truncated transcript has no omission marker")
	}
}

func TestApplicationCompactAppendsCheckpointAndRestoresDerivedContext(
	t *testing.T,
) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspace, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspace, sessionPath, "second prompt", "second answer")

	summaryModel := &controlledModel{
		response:   "checkpoint summary",
		stopReason: llm.StopReasonStop,
		usage: llm.Usage{
			InputTokens:  120,
			OutputTokens: 24,
			TotalTokens:  144,
		},
	}
	command := newCompactTestCommand(t, summaryModel, 1)
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{
		"compact",
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("compact ExecuteContext() error = %v", err)
	}
	if !strings.Contains(output.String(), "retained 1 recent turn(s)") {
		t.Errorf("compact output = %q, want retained-turn count", output.String())
	}

	if len(summaryModel.requests) != 1 {
		t.Fatalf("summary model requests = %d, want 1", len(summaryModel.requests))
	}
	summaryRequest := summaryModel.requests[0]
	if summaryRequest.SystemPrompt != compactionSystemPrompt {
		t.Errorf(
			"summary system prompt = %q, want %q",
			summaryRequest.SystemPrompt,
			compactionSystemPrompt,
		)
	}
	if len(summaryRequest.Tools) != 0 {
		t.Errorf("summary tools = %#v, want none", summaryRequest.Tools)
	}
	if summaryRequest.Options.MaxTokens != defaultCompactionMaxTokens {
		t.Errorf(
			"summary max tokens = %d, want %d",
			summaryRequest.Options.MaxTokens,
			defaultCompactionMaxTokens,
		)
	}
	if len(summaryRequest.Messages) != 1 {
		t.Fatalf("summary messages = %d, want one prompt", len(summaryRequest.Messages))
	}
	summaryPrompt := messageText(t, summaryRequest.Messages[0])
	if !strings.Contains(summaryPrompt, "first prompt") ||
		!strings.Contains(summaryPrompt, "first answer") {
		t.Errorf("summary prompt does not contain first turn: %q", summaryPrompt)
	}
	if strings.Contains(summaryPrompt, "second prompt") {
		t.Errorf("summary prompt contains retained second turn: %q", summaryPrompt)
	}

	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 2 {
		t.Fatalf("turns after compaction = %d, want 2", len(snapshot.Turns))
	}
	if len(snapshot.Compactions) != 1 {
		t.Fatalf(
			"compactions after compaction = %d, want 1",
			len(snapshot.Compactions),
		)
	}
	checkpoint := snapshot.Compactions[0]
	if checkpoint.Summary != "checkpoint summary" {
		t.Errorf("checkpoint summary = %q, want checkpoint summary", checkpoint.Summary)
	}
	if checkpoint.Usage.TotalTokens != 144 {
		t.Errorf("checkpoint usage = %#v, want total 144", checkpoint.Usage)
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(data)), "\n")); got != 4 {
		t.Errorf("physical JSONL records = %d, want header + 2 turns + compaction", got)
	}

	continuationModel := runPrintTurn(
		t,
		workspace,
		sessionPath,
		"third prompt",
		"third answer",
	)
	if len(continuationModel.requests) != 1 {
		t.Fatalf(
			"continuation model requests = %d, want 1",
			len(continuationModel.requests),
		)
	}
	messages := continuationModel.requests[0].Messages
	if len(messages) != 4 {
		t.Fatalf(
			"continuation messages = %d, want summary, retained turn, and prompt",
			len(messages),
		)
	}
	if got := messageText(t, messages[0]); !strings.Contains(got, "checkpoint summary") {
		t.Errorf("derived summary message = %q, want checkpoint summary", got)
	}
	assertTextMessage(t, messages[1], llm.RoleUser, "second prompt")
	assertTextMessage(t, messages[2], llm.RoleAssistant, "second answer")
	assertTextMessage(t, messages[3], llm.RoleUser, "third prompt")

	updatedSummaryModel := &controlledModel{
		response:   "updated checkpoint",
		stopReason: llm.StopReasonStop,
	}
	secondCompact := newCompactTestCommand(t, updatedSummaryModel, 1)
	secondCompact.SetOut(io.Discard)
	secondCompact.SetArgs([]string{
		"compact",
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := secondCompact.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("second compact ExecuteContext() error = %v", err)
	}
	updatedPrompt := messageText(t, updatedSummaryModel.requests[0].Messages[0])
	if !strings.Contains(updatedPrompt, "checkpoint summary") ||
		!strings.Contains(updatedPrompt, "second prompt") {
		t.Errorf("iterative compaction prompt = %q, want old summary and new turn", updatedPrompt)
	}
	if strings.Contains(updatedPrompt, "third prompt") {
		t.Errorf("iterative compaction prompt contains retained third turn: %q", updatedPrompt)
	}

	snapshot = openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 3 {
		t.Fatalf("turns after second compaction = %d, want 3", len(snapshot.Turns))
	}
	if len(snapshot.Compactions) != 2 {
		t.Fatalf(
			"compactions after second compaction = %d, want 2",
			len(snapshot.Compactions),
		)
	}
	latest := snapshot.Compactions[1]
	if latest.FirstKeptTurnID != snapshot.Turns[2].ID ||
		latest.ActiveTurnCount != 2 ||
		latest.RetainedTurnCount != 1 {
		t.Errorf(
			"latest boundary = %#v, want third turn retained from two active turns",
			latest,
		)
	}
}

func TestApplicationPrintAutomaticallyCompactsBeforeRequest(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	modelInfo := llm.Model{
		ID:            "test-model",
		Name:          "Test model",
		API:           llm.API("test-api"),
		Provider:      llm.ProviderID("test-provider"),
		ContextWindow: 10_000,
		MaxTokens:     1_000,
	}
	store := createAppTestSession(t, sessionPath, workspace)
	firstPrompt, err := llm.NewUserMessage(llm.NewTextContent("first prompt").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	firstPrompt.Timestamp = 1
	firstAnswer := llm.NewAssistantMessage(modelInfo)
	firstAnswer.Content = []llm.ContentPart{llm.NewTextContent("first answer").Part()}
	firstAnswer.StopReason = llm.StopReasonStop
	firstAnswer.Timestamp = 2
	firstAnswer.Usage = llm.Usage{TotalTokens: 9_000}
	if err := appendSessionTurn(t.Context(), store, []llm.AgentMessage{
		firstPrompt,
		firstAnswer,
	}); err != nil {
		t.Fatalf("append first turn: %v", err)
	}
	secondPrompt, err := llm.NewUserMessage(llm.NewTextContent("second prompt").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	secondPrompt.Timestamp = 3
	secondAnswer := llm.NewAssistantMessage(modelInfo)
	secondAnswer.Content = []llm.ContentPart{llm.NewTextContent("second answer").Part()}
	secondAnswer.StopReason = llm.StopReasonStop
	secondAnswer.Timestamp = 4
	secondAnswer.Usage = llm.Usage{TotalTokens: 8_000}
	if err := appendSessionTurn(t.Context(), store, []llm.AgentMessage{
		secondPrompt,
		secondAnswer,
	}); err != nil {
		t.Fatalf("append second turn: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	model := &controlledModel{
		response:   "checkpoint summary",
		stopReason: llm.StopReasonStop,
	}
	candidate := &compactTestProvider{model: modelInfo, service: model}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Provider: string(modelInfo.Provider),
				Model:    modelInfo.ID,
			}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		providers:                  []provider.Provider{candidate},
		compactionKeepRecentTokens: 1,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "continue",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("print ExecuteContext() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want compaction and continuation", len(model.requests))
	}
	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Compactions) != 1 {
		t.Fatalf("compactions = %d, want automatic checkpoint", len(snapshot.Compactions))
	}
	if got := messageText(t, model.requests[1].Messages[0]); !strings.Contains(got, "checkpoint summary") {
		t.Errorf("continuation summary = %q", got)
	}
}

func TestApplicationPrintCompactsAnOversizedTurn(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	modelInfo := llm.Model{
		ID:            "test-model",
		Name:          "Test model",
		API:           llm.API("test-api"),
		Provider:      llm.ProviderID("test-provider"),
		ContextWindow: 10_000,
		MaxTokens:     1_000,
	}
	store := createAppTestSession(t, sessionPath, workspace)
	prompt, err := llm.NewUserMessage(llm.NewTextContent("inspect the repository").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	prompt.Timestamp = 1
	answer := llm.NewAssistantMessage(modelInfo)
	answer.Content = []llm.ContentPart{
		llm.NewTextContent(strings.Repeat("large tool transcript ", 8_000)).Part(),
	}
	answer.StopReason = llm.StopReasonStop
	answer.Timestamp = 2
	if err := appendSessionTurn(t.Context(), store, []llm.AgentMessage{prompt, answer}); err != nil {
		t.Fatalf("append oversized turn: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	model := &controlledModel{
		response:   "oversized turn summary",
		stopReason: llm.StopReasonStop,
	}
	candidate := &compactTestProvider{model: modelInfo, service: model}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Provider: string(modelInfo.Provider),
				Model:    modelInfo.ID,
			}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		providers:                  []provider.Provider{candidate},
		compactionKeepRecentTokens: 20_000,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "continue",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("print ExecuteContext() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want oversized compaction and continuation", len(model.requests))
	}
	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Compactions) != 1 || snapshot.Compactions[0].FirstKeptTurnID != "" {
		t.Fatalf("compactions = %#v, want full-branch checkpoint", snapshot.Compactions)
	}
}

func TestApplicationCompactDoesNotCreateMissingSession(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "missing.jsonl")
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			t.Fatal("configuration loaded for missing Session")
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model created for missing Session")
			return nil, nil
		},
		compactionKeepRecentTokens: 1,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetArgs([]string{
		"compact",
		"--workspace", workspace,
		"--session", sessionPath,
	})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ExecuteContext() error = %v, want os.ErrNotExist", err)
	}
	if _, statErr := os.Stat(sessionPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Stat() error = %v, want missing Session", statErr)
	}
}

func TestApplicationCompactRejectsUnsafeSummaryWithoutAppending(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("provider disconnected")
	tests := []struct {
		name  string
		model *controlledModel
		want  string
	}{
		{
			name:  "provider failure",
			model: &controlledModel{err: wantErr},
			want:  wantErr.Error(),
		},
		{
			name: "length truncated",
			model: &controlledModel{
				response:   "partial summary",
				stopReason: llm.StopReasonLength,
			},
			want: `model stopped with reason "length"`,
		},
		{
			name: "empty visible text",
			model: &controlledModel{
				response:   "   ",
				stopReason: llm.StopReasonStop,
			},
			want: "model returned no visible text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workspace := t.TempDir()
			sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
			runPrintTurn(t, workspace, sessionPath, "first prompt", "first answer")
			runPrintTurn(t, workspace, sessionPath, "second prompt", "second answer")

			command := newCompactTestCommand(t, tt.model, 1)
			command.SetOut(io.Discard)
			command.SetArgs([]string{
				"compact",
				"--workspace", workspace,
				"--session", sessionPath,
			})
			err := command.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ExecuteContext() error = %v, want text %q", err, tt.want)
			}

			snapshot := openSessionSnapshot(t, sessionPath)
			if len(snapshot.Turns) != 2 {
				t.Errorf("turns after failed compaction = %d, want 2", len(snapshot.Turns))
			}
			if len(snapshot.Compactions) != 0 {
				t.Errorf(
					"compactions after failed compaction = %#v, want none",
					snapshot.Compactions,
				)
			}
		})
	}
}

func TestApplicationCompactRejectsNothingToCompactBeforeCreatingModel(
	t *testing.T,
) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspace, sessionPath, "only prompt", "only answer")

	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			t.Fatal("configuration loaded with nothing to compact")
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model created with nothing to compact")
			return nil, nil
		},
		compactionKeepRecentTokens: 1,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetArgs([]string{
		"compact",
		"--workspace", workspace,
		"--session", sessionPath,
	})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, session.ErrNothingToCompact) {
		t.Fatalf("ExecuteContext() error = %v, want ErrNothingToCompact", err)
	}
	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Compactions) != 0 {
		t.Errorf("compactions = %#v, want none", snapshot.Compactions)
	}
}

func TestApplicationCompactUsesOnlyActiveBranch(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspace, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspace, sessionPath, "old branch", "old answer")
	snapshot := openSessionSnapshot(t, sessionPath)

	checkout := newSessionTestCommand(t)
	checkout.SetOut(io.Discard)
	checkout.SetArgs([]string{
		"session", "checkout",
		"--workspace", workspace,
		"--session", sessionPath,
		"--entry", snapshot.Turns[0].ID,
	})
	if err := checkout.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("checkout ExecuteContext() error = %v", err)
	}
	runPrintTurn(t, workspace, sessionPath, "new branch", "new answer")

	summaryModel := &controlledModel{
		response:   "active branch checkpoint",
		stopReason: llm.StopReasonStop,
	}
	compact := newCompactTestCommand(t, summaryModel, 1)
	compact.SetOut(io.Discard)
	compact.SetArgs([]string{
		"compact",
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := compact.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("compact ExecuteContext() error = %v", err)
	}
	prompt := messageText(t, summaryModel.requests[0].Messages[0])
	if !strings.Contains(prompt, "first prompt") {
		t.Fatalf("compaction prompt = %q, want active ancestor", prompt)
	}
	if strings.Contains(prompt, "old branch") {
		t.Fatalf("compaction prompt = %q, contains abandoned branch", prompt)
	}
	if strings.Contains(prompt, "new branch") {
		t.Fatalf("compaction prompt = %q, contains retained active turn", prompt)
	}

	snapshot = openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 3 {
		t.Fatalf("turns after branch compaction = %d, want all source turns", len(snapshot.Turns))
	}
	if len(snapshot.Compactions) != 1 {
		t.Fatalf("compactions = %d, want 1", len(snapshot.Compactions))
	}
	checkpoint := snapshot.Compactions[0]
	if checkpoint.ParentID != snapshot.Turns[2].ID ||
		checkpoint.FirstKeptTurnID != snapshot.Turns[2].ID {
		t.Fatalf("branch checkpoint = %#v, want current branch boundary", checkpoint)
	}
	contextMessages, err := session.BuildContext(snapshot)
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if len(contextMessages) != 3 {
		t.Fatalf("context messages = %d, want summary and retained turn", len(contextMessages))
	}
	if summary, ok := contextMessages[0].(llm.CompactionSummaryMessage); !ok ||
		summary.Summary != "active branch checkpoint" {
		t.Fatalf("context summary = %#v", contextMessages[0])
	}
	assertTextMessage(t, contextMessages[1], llm.RoleUser, "new branch")
	assertTextMessage(t, contextMessages[2], llm.RoleAssistant, "new answer")
}

func runPrintTurn(
	t *testing.T,
	workspace string,
	sessionPath string,
	prompt string,
	response string,
) *recordingModel {
	t.Helper()

	model := &recordingModel{response: response}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		compactionKeepRecentTokens: 1,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", prompt,
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("print ExecuteContext() error = %v", err)
	}
	return model
}

func newCompactTestCommand(
	t *testing.T,
	model agent.Model,
	keepRecentTokens int64,
) *cobra.Command {
	t.Helper()

	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		compactionKeepRecentTokens: keepRecentTokens,
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	return command
}

func messageText(t *testing.T, message llm.Message) string {
	t.Helper()

	var content []llm.ContentPart
	switch value := message.(type) {
	case llm.UserMessage:
		content = value.Content
	case llm.AssistantMessage:
		content = value.Content
	default:
		t.Fatalf("message = %T, want user or assistant", message)
	}
	var text strings.Builder
	for _, part := range content {
		if part.Type == llm.ContentTypeText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

type compactTestProvider struct {
	model   llm.Model
	service agent.Model
}

func (p *compactTestProvider) ProviderID() llm.ProviderID {
	return p.model.Provider
}

func (p *compactTestProvider) Label() string {
	return "Test provider"
}

func (p *compactTestProvider) MenuDescription() string {
	return "Test provider"
}

func (p *compactTestProvider) Models() []llm.Model {
	return []llm.Model{p.model}
}

func (p *compactTestProvider) DefaultModel() llm.Model {
	return p.model
}

func (p *compactTestProvider) Configured(config.Config) bool {
	return true
}

func (p *compactTestProvider) New(config.Config) (agent.Model, error) {
	return p.service, nil
}

func (p *compactTestProvider) SaveAPIKey(string) (string, error) {
	return "", nil
}

func (p *compactTestProvider) ApplyAPIKey(*config.Config, string) {}

func (p *compactTestProvider) CredentialNotConfiguredError() error {
	return errors.New("test provider credential is not configured")
}

type controlledModel struct {
	response   string
	stopReason llm.StopReason
	usage      llm.Usage
	err        error
	requests   []llm.Request
}

func (m *controlledModel) Stream(
	_ context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.requests = append(m.requests, request)
	if m.err != nil {
		return nil, m.err
	}

	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{
		llm.NewTextContent(m.response).Part(),
	}
	message.StopReason = m.stopReason
	message.Usage = m.usage
	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{
			Type:         llm.EventTypeTextDelta,
			ContentIndex: 0,
			Delta:        m.response,
		},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{
			Type:       llm.EventTypeDone,
			StopReason: m.stopReason,
			Message:    &message,
		},
	}}, nil
}
