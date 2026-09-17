package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func customLoginCommand() SlashCommand {
	return SlashCommand{Name: "login", SecretPrompt: "API key", Menu: &SlashCommandMenu{
		Title: "Select authentication method", Options: []SlashCommandOption{{
			Label: "Sign in with an API key", Menu: &SlashCommandMenu{
				Title: "Select API key provider", Options: []SlashCommandOption{{
					Label: "Custom", Arguments: "custom", Menu: &SlashCommandMenu{
						Title: "Custom credential", Options: []SlashCommandOption{
							{Label: "Use saved credential", Arguments: "custom", UseSavedCredential: true},
							{Label: "Enter a new API key", Arguments: "custom"},
						},
					},
				}},
			},
		}},
	}}
}

func enterCustomLoginForm(t *testing.T, requests chan runRequest) model {
	t.Helper()
	m := newModel(requests, make(chan struct{}), customLoginCommand())
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.input.SetValue("/login")
	for _, title := range []string{"Select authentication method", "Select API key provider", "Custom credential"} {
		var cmd tea.Cmd
		m, cmd, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil || m.secretInput != nil || m.commandMenu == nil ||
			m.commandMenu.frames[len(m.commandMenu.frames)-1].menu.Title != title {
			t.Fatalf("login did not wait for confirmation at %s", title)
		}
	}
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.secretInput == nil || m.secretInput.prompt != "Custom endpoint URL" {
		t.Fatal("replacement action did not open Custom form")
	}
	return m
}

func TestCustomLoginFormSubmitsSeparateFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, endpoint, key, model string
	}{
		{name: "configured", endpoint: "https://custom.example/v1", key: "private-form-key", model: "Org/Model.v1"},
		{name: "keyless defaults"},
		{name: "model only", model: "qwen3:8b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan runRequest, 1)
			m := enterCustomLoginForm(t, requests)
			var cmd tea.Cmd
			for step, value := range []string{tc.endpoint, tc.key, tc.model} {
				m.input.SetValue(value)
				if value != "" && strings.Contains(m.View().Content, value) {
					t.Fatal("form value is visible")
				}
				m, cmd, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
				if step < 2 && (cmd != nil || m.secretInput == nil) {
					t.Fatal("form submitted before all steps were confirmed")
				}
			}
			if cmd == nil {
				t.Fatal("completed form did not submit")
			}
			cmd()
			req := (<-requests).command
			if req == nil || req.Arguments != "custom" || req.CustomEndpoint != tc.endpoint ||
				req.CustomModel != tc.model || req.Secret != tc.key {
				t.Fatal("form fields were not delivered separately")
			}
			if m.secretInput != nil || m.customLogin != nil || m.input.Value() != "" {
				t.Fatal("completed form retained hidden input")
			}
			for _, value := range []string{tc.endpoint, tc.key, tc.model} {
				if value != "" && (strings.Contains(m.transcriptView(), value) || strings.Contains(strings.Join(m.promptHistory, "\n"), value)) {
					t.Fatal("form value leaked into history")
				}
			}
		})
	}
}

func TestCustomLoginFormCancellationDiscardsEveryStep(t *testing.T) {
	t.Parallel()
	for step, name := range []string{"endpoint", "key", "model"} {
		t.Run(name, func(t *testing.T) {
			requests := make(chan runRequest, 1)
			m := enterCustomLoginForm(t, requests)
			for _, value := range []string{"https://private.example/v1", "private-key"}[:step] {
				m.input.SetValue(value)
				m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			m.input.SetValue("discarded-value")
			m, _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.secretInput != nil || m.customLogin != nil || m.input.Value() != "" || len(requests) != 0 {
				t.Fatal("cancelled form retained or submitted values")
			}
			for _, value := range []string{"private.example", "private-key", "discarded-value"} {
				if strings.Contains(m.transcriptView(), value) || strings.Contains(strings.Join(m.promptHistory, "\n"), value) {
					t.Fatal("cancelled form leaked into history")
				}
			}
		})
	}
}

func TestCustomLoginShortcutDoesNotSkipMenus(t *testing.T) {
	t.Parallel()
	for _, draft := range []string{"/login custom", "/login custom https://custom.example/v1 model"} {
		t.Run(draft, func(t *testing.T) {
			m := newModel(make(chan runRequest, 1), make(chan struct{}), customLoginCommand())
			m = updateModel(t, m, tea.PasteMsg{Content: draft})
			m, cmd, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd != nil || m.secretInput != nil || m.running || m.commandMenu == nil ||
				len(m.commandMenu.frames) != 1 || len(m.matchingCommandOptions()) != 0 {
				t.Fatal("inline login bypassed the authentication menu")
			}
		})
	}
}
