package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestAccountLoginInputIsTransientAndCancelled(t *testing.T) {
	for _, method := range []string{"browser", "device-code"} {
		t.Run(method, func(t *testing.T) {
			requests := make(chan runRequest, 1)
			m := newModel(requests, make(chan struct{}))
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m, command, _ := m.startApplicationSlashCommand("/login", SlashCommandRequest{Name: "login", Arguments: "openai-codex", LoginMethod: method}, SlashCommand{Name: "login", SecretPrompt: "API key"})
			if m.secretInput != nil || m.authInput == nil || !m.running {
				t.Fatal("account login fell into API key input")
			}
			command()
			request := <-requests
			if request.command.Auth == nil || request.command.LoginMethod != method {
				t.Fatal("missing account interaction")
			}
			cancelled := false
			prompt := interaction.AuthPrompt{Title: "Login to OpenAI Codex", URL: "https://example.test/auth", Code: "ABCD-EFGH", AllowInput: method == "browser"}
			m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{cancel: func() { cancelled = true }}, {auth: &prompt}}})
			if !strings.Contains(m.transcriptView(), prompt.URL) || !strings.Contains(m.transcriptView(), prompt.Code) {
				t.Fatal("authorization instructions not visible")
			}
			m = updateModel(t, m, tea.PasteMsg{Content: "private-authorization-code"})
			if method == "browser" {
				if strings.Contains(m.composerView(80), "private-") {
					t.Fatal("authorization code visible")
				}
				m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
				select {
				case value := <-request.command.Auth.Input:
					if value != "private-authorization-code" {
						t.Fatal("wrong manual input")
					}
				default:
					t.Fatal("manual input not forwarded")
				}
			} else if m.input.Value() != "" {
				t.Fatal("device login accepted chat input")
			}
			if len(m.promptHistory) != 0 || len(m.entries) != 1 || len(m.pendingDeliveries) != 0 {
				t.Fatal("authorization polluted history")
			}
			m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
			if !cancelled || m.composerInputEnabled() {
				t.Fatal("login not cancelled")
			}
			m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{err: context.Canceled, done: true}}})
			if m.authInput != nil || m.authPrompt != nil || m.running || m.input.Value() != "" || !m.input.Focused() {
				t.Fatal("login state survived cancellation")
			}
			if strings.Contains(m.transcriptView(), prompt.Code) || m.status != "Login cancelled" {
				t.Fatal("transient authorization remained visible")
			}
		})
	}
}

func TestAccountLoginCancelBeforeControllerStarts(t *testing.T) {
	m := newModel(make(chan runRequest, 1), make(chan struct{}))
	m.authInput = make(chan string, 1)
	m.running = true
	m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	cancelled := false
	prompt := interaction.AuthPrompt{AllowInput: true}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{cancel: func() { cancelled = true }}, {auth: &prompt}}})
	if !cancelled || m.authPrompt != nil || m.composerInputEnabled() {
		t.Fatal("late startup revived cancelled login")
	}
}

func TestControllerStreamsAuthenticationBeforeCompletion(t *testing.T) {
	input := make(chan string, 1)
	updates := make(chan runUpdate, runUpdateBuffer)
	runner := &slashRunner{runCommand: func(ctx context.Context, request SlashCommandRequest) (string, error) {
		if err := request.Auth.Notify(ctx, interaction.AuthPrompt{URL: "https://example.test", AllowInput: true}); err != nil {
			return "", err
		}
		select {
		case value := <-request.Auth.Input:
			return value, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := SlashCommandRequest{Name: "login", Auth: &interaction.AuthInteraction{Input: input}}
	go func() { finished <- runSlashCommand(ctx, runner, runRequest{command: &request, updates: updates}) }()
	first := <-updates
	if first.cancel == nil {
		t.Fatal("missing command cancellation")
	}
	progress := <-updates
	if progress.auth == nil || !progress.auth.AllowInput {
		t.Fatal("missing live auth progress")
	}
	input <- "signed in"
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	done := <-updates
	if !done.done || done.output != "signed in" {
		t.Fatal("missing login completion")
	}
	if request.Auth.Notify != nil {
		t.Fatal("controller mutated UI interaction")
	}
}
