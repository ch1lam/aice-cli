package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestCommandFlowCredentialValidation(t *testing.T) {
	t.Parallel()
	custom := interaction.CommandRequest{Name: "login", Arguments: "custom"}
	for _, tc := range []struct {
		name    string
		request interaction.CommandRequest
		values  []string
		problem string
	}{
		{name: "blank key", request: interaction.CommandRequest{Name: "login", Arguments: "deepseek"}, values: []string{" "}, problem: "API key must not be blank"},
		{name: "endpoint scheme", request: custom, values: []string{"ftp://example"}, problem: "Endpoint must start with http://"},
		{name: "model whitespace", request: custom, values: []string{"", "", "two words"}, problem: "Model name must not contain whitespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommandFlow("", interaction.Command{Name: "login", SecretPrompt: "API key"}, tc.request)
			f.beginCredential()
			var problem string
			for _, value := range tc.values {
				problem, _ = f.submitCredential(value)
			}
			if !strings.HasPrefix(problem, tc.problem) || f.formStep() != len(tc.values)-1 {
				t.Fatalf("problem = %q at step %d", problem, f.formStep())
			}
		})
	}
}

func TestSettingsCustomLoginFormMasksOnlyTheKey(t *testing.T) {
	t.Parallel()
	m := panelModel(t, 120, 40)
	var started *settingsAction
	m.runSettingsAction = func(a *settingsAction, _ uint64) tea.Cmd { started = a; return nil }
	menu := &interaction.CommandMenu{Title: "Provider", Options: []interaction.CommandOption{{Label: "Custom", Arguments: "custom"}}}
	next, _ := m.openSettingAction(interaction.SettingField{ID: "account.custom", Kind: interaction.SettingAction,
		Action: &interaction.Command{Name: "login", SecretPrompt: "API key", Menu: menu}})
	m = updateModel(t, next.(model), tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, step := range []struct {
		value string
		echo  textinput.EchoMode
	}{{"https://form.example/v1", textinput.EchoNormal}, {"private-key", textinput.EchoPassword}, {"form-model", textinput.EchoNormal}} {
		if m.settings.input.EchoMode != step.echo || !m.settings.input.Focused() {
			t.Fatalf("step %q echo = %v", step.value, m.settings.input.EchoMode)
		}
		m.settings.input.SetValue(step.value)
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if started == nil || started.request.CustomEndpoint != "https://form.example/v1" ||
		started.request.Secret != "private-key" || started.request.CustomModel != "form-model" {
		t.Fatal("Settings form did not deliver separate fields")
	}
	if len(m.entries) != 0 || len(m.promptHistory) != 0 || strings.Contains(m.settingsPanelView(), "private-key") {
		t.Fatal("Settings form leaked a credential")
	}
}
