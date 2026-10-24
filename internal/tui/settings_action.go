package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

// settingsAction hosts a commandFlow in the Settings modal. page scrolls
// long prompt text before its choices become actionable.
type settingsAction struct {
	commandFlow
	wait      tea.Cmd
	page      int
	running   bool
	cancelled bool
	cancel    context.CancelFunc
}
type settingsActionPrompt struct {
	action *settingsAction
	prompt interaction.AuthPrompt
}
type settingsActionDone struct {
	action   *settingsAction
	result   interaction.SettingsActionResult
	snapshot interaction.SettingsSnapshot
	err      error
}

// settingsActionCommands bridges existing domain prompts into the modal's own
// editor. No command, secret or result passes through the composer/transcript.
func settingsActionCommands(parent context.Context, runner interaction.SettingsActionRunner, reader interaction.SettingsReader) (func(*settingsAction, uint64) tea.Cmd, func()) {
	ctx, cancel := context.WithCancel(parent)
	owner := &queryGate{}
	start := func(a *settingsAction, revision uint64) tea.Cmd {
		ctx, stop := context.WithCancel(ctx)
		a.cancel = stop
		a.input = make(chan string, 1)
		prompts := make(chan interaction.AuthPrompt)
		request := a.request
		request.Auth = &interaction.AuthInteraction{Input: a.input, Notify: func(ctx context.Context, p interaction.AuthPrompt) error {
			select {
			case prompts <- p:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		run := func() tea.Msg {
			if !owner.begin() {
				stop()
				return settingsActionDone{action: a, err: context.Canceled}
			}
			defer owner.done()
			defer stop()
			result, err := runner.RunSettingsAction(ctx, revision, request)
			snapshot, _ := reader.ReadSettings(parent)
			return settingsActionDone{action: a, result: result, err: err, snapshot: snapshot}
		}
		a.wait = func() tea.Msg {
			select {
			case prompt := <-prompts:
				return settingsActionPrompt{action: a, prompt: prompt}
			case <-ctx.Done():
				return nil
			}
		}
		return tea.Batch(run, a.wait)
	}
	return start, func() { owner.close(cancel) }
}

func (m model) openSettingAction(field interaction.SettingField) (tea.Model, tea.Cmd) {
	p := m.settings
	if field.Action == nil || m.runSettingsAction == nil {
		p.notice = "This action is unavailable"
		return m, nil
	}
	m.invalidateSettingsRead()
	p.editing = &field
	p.search = false
	p.input.SetValue("")
	p.input.Blur()
	a := &settingsAction{commandFlow: *newCommandFlow("", *field.Action, interaction.CommandRequest{Name: field.Action.Name, Arguments: field.Arguments})}
	p.action = a
	if a.menu() != nil {
		return m, nil
	}
	return m.prepareSettingAction()
}
func (m model) prepareSettingAction() (tea.Model, tea.Cmd) {
	p := m.settings
	a := p.action
	if a.needsCredential() {
		a.beginCredential()
		return m, p.showCredentialStep()
	}
	return m, m.executeSettingAction()
}

// showCredentialStep clears the modal editor for the current form field.
// Only secret fields are masked.
func (p *settingsPanel) showCredentialStep() tea.Cmd {
	p.input.SetValue("")
	p.input.EchoMode = textinput.EchoNormal
	if p.action.credential().secret {
		p.input.EchoMode = textinput.EchoPassword
	}
	return p.input.Focus()
}
func (m *model) executeSettingAction() tea.Cmd {
	p := m.settings
	a := p.action
	a.running = true
	a.prompt = nil
	a.menus = nil
	p.input.SetValue("")
	p.input.Blur()
	p.input.EchoMode = textinput.EchoNormal
	p.notice = "Working… Esc cancels"
	return m.runSettingsAction(a, p.snapshot.Revision)
}
func (m model) settingActionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.settings
	a := p.action
	name := key.String()
	if name == "esc" {
		if a.running {
			a.cancelled = true
			if a.cancel != nil {
				a.cancel()
			}
			p.notice = "Cancelling…"
			p.input.SetValue("")
			p.input.Blur()
			return m, nil
		}
		if a.back() {
			a.page = 0
			return m, nil
		}
		p.action = nil
		p.editing = nil
		p.input.SetValue("")
		p.input.Blur()
		p.input.EchoMode = textinput.EchoNormal
		p.notice = ""
		return m, nil
	}
	if a.cancelled {
		return m, nil
	}
	if menu, selection := a.activeMenu(); menu != nil {
		layout := p.actionMenuLayout(menu, *selection)
		if name == "pgup" {
			a.page = max(0, a.page-1)
			return m, nil
		}
		if layout.more {
			switch name {
			case "enter", "down", "pgdown":
				a.page++
			case "up":
				a.page = max(0, a.page-1)
			}
			return m, nil
		}
		switch name {
		case "up":
			*selection = clampIndex(*selection-1, len(menu.Options))
		case "down":
			*selection = clampIndex(*selection+1, len(menu.Options))
		case "enter":
			if len(menu.Options) == 0 {
				return m, nil
			}
			option := menu.Options[*selection]
			if a.running {
				if a.reply(option.Arguments) {
					a.prompt = nil
					p.notice = "Working…"
				}
				return m, a.wait
			}
			if a.choose(option) {
				a.page = 0
				return m, nil
			}
			return m.prepareSettingAction()
		}
		return m, nil
	}
	if a.form != nil && name == "enter" {
		problem, done := a.submitCredential(p.input.Value())
		p.notice = problem
		switch {
		case problem != "":
			return m, nil
		case done:
			return m, m.executeSettingAction()
		}
		return m, p.showCredentialStep()
	}
	if a.prompt != nil && a.prompt.AllowInput && name == "enter" {
		value := strings.TrimSpace(p.input.Value())
		if strings.ContainsAny(value, "\r\n") {
			p.notice = "Enter one line"
			return m, nil
		}
		if a.reply(value) {
			p.input.SetValue("")
			p.input.Blur()
			a.prompt = nil
			p.notice = "Working…"
		}
		return m, a.wait
	}
	if a.form != nil || a.prompt != nil && a.prompt.AllowInput {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(key)
		return m, m.scopeInputCommand(cmd)
	}
	return m, nil
}
func (m model) applySettingActionPrompt(msg settingsActionPrompt) (tea.Model, tea.Cmd) {
	p := m.settings
	if p == nil || p.action != msg.action || msg.action.cancelled {
		return m, nil
	}
	a := p.action
	a.showPrompt(msg.prompt)
	a.page = 0
	p.input.SetValue("")
	p.notice = ""
	if msg.prompt.AllowInput {
		p.input.EchoMode = textinput.EchoPassword
		if msg.prompt.PublicInput {
			p.input.EchoMode = textinput.EchoNormal
		}
		return m, p.input.Focus()
	}
	p.input.Blur()
	if msg.prompt.Menu == nil {
		return m, a.wait
	}
	return m, nil
}
func (m model) applySettingActionDone(msg settingsActionDone) (tea.Model, tea.Cmd) {
	if msg.snapshot.Runtime.Model.ID != "" {
		m.currentModel = msg.snapshot.Runtime.Model
		m.thinking = msg.snapshot.Runtime.Thinking
		m.apiKeyConfigured = msg.snapshot.Runtime.APIKeyConfigured
		m.contextUsage = msg.snapshot.Runtime.Context
	}
	p := m.settings
	if p == nil || p.action != msg.action {
		return m, nil
	}
	p.action.request.Secret = ""
	p.action = nil
	p.editing = nil
	p.input.SetValue("")
	p.input.Blur()
	p.input.EchoMode = textinput.EchoNormal
	if msg.snapshot.Categories != nil {
		p.snapshot = msg.snapshot
	}
	if msg.result.FocusSetting != "" && msg.err == nil && !msg.action.cancelled {
		p.focusField = msg.result.FocusSetting
		p.search = false
		p.notice = ""
		return m, m.refreshSettings()
	}
	p.notice = settingsActionNotice(msg.result, msg.err)
	if strings.Contains(p.notice, "\n") {
		id := ""
		if msg.action.command.Name == "mcp" {
			id = "mcp.services"
		}
		p.editing = &interaction.SettingField{ID: id, Kind: interaction.SettingInfo, Label: "Action result", Description: p.notice}
		p.detailOffset = 0
	}
	return m, nil
}

func settingsActionNotice(result interaction.SettingsActionResult, err error) string {
	var lines []string
	if result.Output != "" {
		lines = append(lines, result.Output)
	}
	for _, step := range result.External {
		status := "Incomplete"
		if step.Completed {
			status = "Completed"
		}
		lines = append(lines, status+": "+step.Name+". "+step.Detail)
	}
	if result.Committed {
		lines = append(lines, "Preference saved")
	}
	if result.ReadinessKnown {
		if result.Ready {
			lines = append(lines, "Ready")
		} else {
			lines = append(lines, "Not ready; check setup status")
		}
	}
	lines = append(lines, result.Warnings...)
	if err != nil {
		lines = append(lines, err.Error())
	}
	return strings.Join(lines, "\n")
}
func (p *settingsPanel) actionView() string {
	a := p.action
	if a.form != nil {
		return sanitizeSingleLineText(a.credential().title()) + "\n" + p.input.View()
	}
	title := "Working… Esc cancels"
	if q := a.prompt; q != nil {
		title = strings.Join([]string{q.Title, q.Instructions, q.URL, q.Code}, "\n")
		if q.AllowInput {
			return sanitizeSingleLineText(q.Title) + "\n" + p.input.View() + "\n\n" + sanitizeMultilineText(q.Instructions) + "\n" + sanitizeSingleLineText(q.URL) + "\n" + sanitizeSingleLineText(q.Code)
		}
	}
	menu, selection := a.activeMenu()
	if menu == nil {
		return sanitizeMultilineText(title)
	}
	layout := p.actionMenuLayout(menu, *selection)
	rows := append([]string(nil), layout.header...)
	if layout.more {
		return strings.Join(append(rows, ansi.Truncate("Enter: more · PgUp: back", max(1, p.layout.inner), "…")), "\n")
	}
	for i := layout.start; i < layout.end; i++ {
		v := menu.Options[i]
		prefix := menuPrefix(i == *selection)
		row := sanitizeSingleLineText(fmt.Sprint(prefix, v.Label, "  ", v.Description))
		if a.command.Name == "mcp" {
			style := bodyStyle
			if i == *selection {
				style = labelStyle
			}
			row = style.Render(sanitizeSingleLineText(prefix + v.Label))
			if v.Description != "" {
				row += mutedStyle.Render("  " + sanitizeSingleLineText(v.Description))
			}
		}
		rows = append(rows, ansi.Truncate(row, max(1, p.layout.inner), "…"))
	}
	return strings.Join(rows, "\n")
}

type actionMenuLayout struct {
	header     []string
	more       bool
	start, end int
}

// Layout and hit testing share the wrapped disclosure. Long instructions are
// paged before choices become actionable, rather than clipped above a consent.
func (p *settingsPanel) actionMenuLayout(menu *interaction.CommandMenu, selection int) actionMenuLayout {
	a := p.action
	parts := []string{menu.Title}
	if q := a.prompt; q != nil {
		parts = nil
		for _, text := range []string{q.Title, q.Instructions, q.URL, q.Code} {
			if text != "" {
				parts = append(parts, text)
			}
		}
		if menu.Title != q.Title {
			parts = append(parts, menu.Title)
		}
	}
	lines := strings.Split(ansi.Hardwrap(sanitizeMultilineText(strings.Join(parts, "\n")), max(1, p.layout.inner), true), "\n")
	pageSize := max(1, p.layout.bodyHeight-1-max(1, min(len(menu.Options), 3)))
	start := min(a.page*pageSize, ((len(lines)-1)/pageSize)*pageSize)
	end := min(len(lines), start+pageSize)
	layout := actionMenuLayout{header: lines[start:end], more: end < len(lines)}
	available := max(1, p.layout.bodyHeight-len(layout.header))
	layout.start, layout.end = listWindow(selection, available, len(menu.Options))
	return layout
}
