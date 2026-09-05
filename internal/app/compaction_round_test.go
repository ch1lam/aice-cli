package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

func TestPrintCompactsToolRoundsWithAndWithoutSession(t *testing.T) {
	t.Parallel()
	for _, persistent := range []bool{false, true} {
		name := "memory"
		if persistent {
			name = "session"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			writeAppFile(t, workspace, "large.txt", strings.Repeat("x", 14000))
			path := filepath.Join(t.TempDir(), "session.jsonl")
			info := llm.Model{ID: "test", Name: "Test", API: "test-api", Provider: "test-provider", ContextWindow: 10000, MaxTokens: 1000}
			service := &roundCompactionModel{}
			configuration, home := compactPrintConfig(t, info)
			command, err := newTestCommand(t, dependencies{
				loadConfig:  func() (config.Config, error) { return configuration, nil },
				newModel:    func(config.Config) (agent.Model, error) { return service, nil },
				providers:   []provider.Provider{&compactTestProvider{model: info, service: service}},
				userHomeDir: func() (string, error) { return home, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--workspace", workspace, "--print", "inspect large.txt", "--yolo"}
			if persistent {
				args = append(args, "--session", path)
			}
			command.SetArgs(args)
			command.SetOut(io.Discard)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if service.mainCalls != 3 || service.summaries != 1 {
				t.Fatalf("main/summary=%d/%d", service.mainCalls, service.summaries)
			}
			if len(service.final.Messages) != 1 || !strings.Contains(messageText(t, service.final.Messages[0]), "large file inspected") {
				t.Fatalf("continuation did not use compacted projection: %#v", service.final.Messages)
			}
			if _, err := os.Stat(filepath.Join(workspace, "must-not-exist")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed retry tool executed: %v", err)
			}
			if persistent {
				snapshot := openSessionSnapshot(t, path)
				// user, failed call+synthetic result, successful call+actual result, final assistant.
				if len(snapshot.Messages) != 6 || len(snapshot.Compactions) != 1 {
					t.Fatalf("source messages/checkpoints=%d/%d", len(snapshot.Messages), len(snapshot.Compactions))
				}
				if snapshot.Messages[1].Message.(llm.AssistantMessage).StopReason != llm.StopReasonError {
					t.Fatal("source failed attempt lost")
				}
			} else {
				if _, err := os.Stat(filepath.Join(workspace, ".aice", "sessions")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stateless run created session directory: %v", err)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stateless run created session: %v", err)
				}
			}
		})
	}
}

type roundCompactionModel struct {
	mainCalls int
	summaries int
	final     llm.Request
}

func (m *roundCompactionModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.SystemPrompt == compactionSystemPrompt {
		m.summaries++
		model := &controlledModel{response: "large file inspected", stopReason: llm.StopReasonStop}
		return model.Stream(ctx, request)
	}
	m.mainCalls++
	message := llm.NewAssistantMessage(request.Model)
	message.StopReason = llm.StopReasonToolUse
	call := llm.ToolCall{ID: "read-1", Name: "read", Arguments: []byte(`{"path":"large.txt"}`)}
	if m.mainCalls == 1 {
		call = llm.ToolCall{ID: "failed-write", Name: "write", Arguments: []byte(`{"path":"must-not-exist","content":"bad"}`)}
	}
	message.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &call}}
	message.Usage = llm.Usage{TotalTokens: 9000}
	terminal := llm.Event{Type: llm.EventTypeDone, StopReason: message.StopReason, Message: &message}
	if m.mainCalls == 1 {
		message.StopReason = llm.StopReasonError
		message.ErrorMessage = "temporary"
		terminal.Type, terminal.StopReason = llm.EventTypeError, llm.StopReasonError
		terminal.Err = &llm.ProviderError{StatusCode: 503, Err: errors.New("temporary")}
	} else if m.mainCalls == 3 {
		m.final = request
		message.StopReason = llm.StopReasonStop
		message.Content = []llm.ContentPart{llm.NewTextContent("done").Part()}
		terminal.StopReason = message.StopReason
	} else if m.mainCalls > 3 {
		return nil, errors.New("unexpected continuation")
	}
	return &eventStream{events: []llm.Event{{Type: llm.EventTypeStart}, terminal}}, nil
}
