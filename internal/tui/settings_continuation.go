package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func (m model) canContinueTask() bool {
	p := m.settings
	return p != nil && p.continuation != nil && p.editing != nil && p.editing.Kind == interaction.SettingInfo &&
		!p.usage && !p.loading && !p.saving && p.action == nil &&
		!m.running && !m.controllerClosed
}

// This consumes only the explicit proposal. Composer contents remain editable
// and queued deliveries from the preceding run never enter the new request.
func (m model) continueTask() (tea.Model, tea.Cmd) {
	if !m.canContinueTask() {
		return m, nil
	}
	value := *m.settings.continuation
	m.closeSettings()
	m.promptHistory = appendPromptHistory(m.promptHistory, value.Prompt)
	m.historyIndex = -1
	m.historyDraft = ""
	m.submittedDraft = composerDraft{}
	next, cmd, _ := m.beginSubmittedRun(RunInput{Prompt: value.Prompt, Continuation: &value})
	return next, cmd
}
