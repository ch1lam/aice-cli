package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestMCPTransientPublicAndSecretInputs(t *testing.T) {
	requests := make(chan runRequest, 1)
	m := newModel(requests, make(chan struct{}))
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 35})
	m.commands = slashCommandCatalog([]SlashCommand{{Name: "mcp", Interactive: true}})
	m, cmd, handled := m.submitSlashCommand("/mcp add", SlashCommandRequest{Name: "mcp", Arguments: "add"})
	if !handled || cmd == nil {
		t.Fatal("command not started")
	}
	cmd()
	request := <-requests
	for _, step := range []struct {
		public bool
		value  string
	}{{true, "public-service-id"}, {false, "private-token-fixture"}} {
		prompt := interaction.AuthPrompt{Title: "MCP input", AllowInput: true, PublicInput: step.public}
		m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{auth: &prompt}}})
		m = updateModel(t, m, tea.PasteMsg{Content: step.value})
		view := ansi.Strip(m.View().Content)
		if strings.Contains(view, step.value) != step.public {
			t.Fatalf("wrong visibility for public=%t", step.public)
		}
		m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if value := <-request.command.Auth.Input; value != step.value {
			t.Fatal("input changed")
		}
		if strings.Contains(m.transcriptView(), step.value) || len(m.promptHistory) != 0 {
			t.Fatal("transient input became history")
		}
	}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true}}})
	if m.authInput != nil || m.authPrompt != nil || m.input.Value() != "" {
		t.Fatal("private editor survived completion")
	}
}

func TestMCPSettingsPromptVisibility(t *testing.T) {
	m := newModel(nil, nil)
	m.settings = &settingsPanel{input: textinput.New(), action: &settingsAction{}}
	for _, public := range []bool{true, false} {
		updated, _ := m.applySettingActionPrompt(settingsActionPrompt{action: m.settings.action, prompt: interaction.AuthPrompt{AllowInput: true, PublicInput: public}})
		m = updated.(model)
		m.settings.input.SetValue("field-content")
		visible := strings.Contains(ansi.Strip(m.settings.input.View()), "field-content")
		if visible != public {
			t.Fatalf("Settings public=%t visible=%t", public, visible)
		}
	}
}
