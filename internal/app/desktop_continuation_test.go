package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func desktopContinuationFixture(t *testing.T) (*interactiveSession, *recordingModel, *interaction.TaskContinuation) {
	t.Helper()
	s := desktopSettingsSession(t)
	s.desktop.bind = func(ctx context.Context, _ desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
		return &appDesktopBackend{}, func() error { return nil }, nil
	}
	model := &recordingModel{response: "continued"}
	s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return model, nil }
	if err := s.ensureSessionStore(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.conversation.store.Close() })
	prompt, err := llm.NewUserMessage(llm.NewTextContent("Existing task, with recorded progress").Part())
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionMessage(t.Context(), s.conversation.store, prompt); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.conversation.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	_, auth := newScriptedUI("continue")
	result, err := s.RunSettingsAction(t.Context(), 0, interaction.CommandRequest{Name: "desktop", Arguments: "setup", Auth: auth})
	if err != nil || result.Continuation == nil || result.Continuation.Revision != result.Revision {
		t.Fatalf("proposal=%+v err=%v", result, err)
	}
	after, err := os.ReadFile(s.conversation.store.Path())
	if err != nil || !bytes.Equal(before, after) || len(model.requests) != 0 {
		t.Fatal("setup changed history or started a model", err)
	}
	return s, model, result.Continuation
}

func TestDesktopContinuationPreservesContextAndRejectsReuse(t *testing.T) {
	s, model, value := desktopContinuationFixture(t)
	input := interaction.RunInput{Prompt: value.Prompt, Continuation: value}
	first, err := s.NewRun(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.NewRun(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Prepared runs own copies, independent of the frontend's proposal.
	value.LeafID = "changed by frontend"
	if err := first.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("requests=%d", len(model.requests))
	}
	messages := model.requests[0].Messages
	if len(messages) != 2 || messageText(t, messages[0]) != "Existing task, with recorded progress" || messageText(t, messages[1]) != desktopContinuePrompt {
		t.Fatalf("continuation lost or replayed task context: %+v", messages)
	}
	snapshot, err := s.conversation.store.Snapshot()
	if err != nil || len(snapshot.Messages) != 3 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if err := second.Run(t.Context()); !errors.Is(err, errDesktopContinuationStale) {
		t.Fatalf("reused proposal=%v", err)
	}
	if len(model.requests) != 1 {
		t.Fatal("stale continuation reached model")
	}
	if _, reason := s.settingsStatus(); reason != "" {
		t.Fatal("reservation leaked", reason)
	}
}

func TestDesktopPreparedContinuationRejectsLaterSettings(t *testing.T) {
	s, model, value := desktopContinuationFixture(t)
	run, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: value.Prompt, Continuation: value}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A restart-only setting does not invalidate ordinary prepared resources,
	// but must invalidate a proposal tied to the earlier Settings result.
	_, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Revision: value.Revision, Changes: []interaction.SettingChange{{ID: "no_update_check", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Run(t.Context()); !errors.Is(err, errDesktopContinuationStale) {
		t.Fatalf("stale prepared continuation=%v", err)
	}
	if len(model.requests) != 0 {
		t.Fatal("stale continuation reached model")
	}
}

func TestDesktopContinuationRejectsChangedInputAndTask(t *testing.T) {
	for _, change := range []string{"prompt", "files", "session", "leaf", "revision", "disabled"} {
		t.Run(change, func(t *testing.T) {
			s, model, value := desktopContinuationFixture(t)
			input := interaction.RunInput{Prompt: value.Prompt, Continuation: value}
			switch change {
			case "prompt":
				input.Prompt = "different request"
			case "files":
				input.Files = []string{"must-not-read"}
			case "session":
				value.SessionID = "different session"
			case "leaf":
				value.LeafID = "different branch"
			case "revision":
				value.Revision++
			case "disabled":
				s.configuration.DesktopEnabled = false
			}
			if _, err := s.NewRun(t.Context(), input, nil); !errors.Is(err, errDesktopContinuationStale) {
				t.Fatalf("changed %s accepted: %v", change, err)
			}
			if len(model.requests) != 0 {
				t.Fatal("rejected input reached model")
			}
		})
	}
}

func TestDesktopContinuationDoesNotCreateEmptyTask(t *testing.T) {
	s := desktopSettingsSession(t)
	if s.desktopContinuation() != nil || s.conversation.store != nil {
		t.Fatal("proposal created an empty Session")
	}
	if err := s.ensureSessionStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.conversation.store.Close()
	if s.desktopContinuation() != nil {
		t.Fatal("empty Session offered continuation")
	}
}
