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

type settingsAction struct {
	wait               tea.Cmd
	command            interaction.Command
	request            interaction.CommandRequest
	menus              []*interaction.CommandMenu
	prompt             *interaction.AuthPrompt
	choice, customStep int
	page               int
	secret             bool
	running            bool
	cancelled          bool
	cancel             context.CancelFunc
	input              chan string
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
	owner := &settingsQueryOwner{}
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
			defer owner.wg.Done()
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
	return start, func() { owner.mu.Lock(); owner.closed = true; owner.mu.Unlock(); cancel(); owner.wg.Wait() }
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
	a := &settingsAction{command: *field.Action, request: interaction.CommandRequest{Name: field.Action.Name, Arguments: field.Arguments}, customStep: -1}
	p.action = a
	if field.Action.Menu != nil {
		a.menus = []*interaction.CommandMenu{field.Action.Menu}
		return m, nil
	}
	return m.prepareSettingAction()
}
func (m model) prepareSettingAction() (tea.Model, tea.Cmd) {
	p := m.settings
	a := p.action
	if a.command.SecretPrompt != "" && !a.request.UseSavedCredential && a.request.LoginMethod == "" {
		a.secret = true
		title := a.command.SecretPrompt
		if a.request.Arguments == "custom" {
			a.customStep = 0
			title = "Custom endpoint (empty keeps current)"
		}
		a.prompt = &interaction.AuthPrompt{Title: title, AllowInput: true}
		p.input.SetValue("")
		p.input.EchoMode = textinput.EchoPassword
		if a.customStep == 0 {
			p.input.EchoMode = textinput.EchoNormal
		}
		return m, p.input.Focus()
	}
	return m, m.executeSettingAction()
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
		if !a.secret && len(a.menus) > 1 {
			a.menus = a.menus[:len(a.menus)-1]
			a.choice = 0
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
	var menu *interaction.CommandMenu
	if a.prompt != nil {
		menu = a.prompt.Menu
	} else if len(a.menus) > 0 {
		menu = a.menus[len(a.menus)-1]
	}
	if menu != nil {
		layout := p.actionMenuLayout(menu)
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
			a.choice = max(0, a.choice-1)
		case "down":
			a.choice = min(max(0, len(menu.Options)-1), a.choice+1)
		case "enter":
			if len(menu.Options) == 0 {
				return m, nil
			}
			option := menu.Options[a.choice]
			if a.running {
				select {
				case a.input <- option.Arguments:
					a.prompt = nil
					p.notice = "Working…"
				default:
				}
				return m, a.wait
			}
			if option.Menu != nil {
				a.menus = append(a.menus, option.Menu)
				a.choice = 0
				a.page = 0
				return m, nil
			}
			a.request.Arguments = option.Arguments
			a.request.UseSavedCredential = option.UseSavedCredential
			a.request.LoginMethod = option.LoginMethod
			a.menus = nil
			return m.prepareSettingAction()
		}
		return m, nil
	}
	if a.prompt != nil && a.prompt.AllowInput {
		if name == "enter" {
			value := strings.TrimSpace(p.input.Value())
			if strings.ContainsAny(value, "\r\n") {
				p.notice = "Enter one line"
				return m, nil
			}
			if a.secret {
				if a.customStep >= 0 {
					switch a.customStep {
					case 0:
						a.request.CustomEndpoint = value
						a.customStep = 1
						a.prompt.Title = "API key (empty for a local service)"
						p.input.EchoMode = textinput.EchoPassword
					case 1:
						a.request.Secret = value
						a.customStep = 2
						a.prompt.Title = "Model ID (empty keeps current)"
						p.input.EchoMode = textinput.EchoNormal
					case 2:
						a.request.CustomModel = value
						return m, m.executeSettingAction()
					}
					p.input.SetValue("")
					return m, p.input.Focus()
				}
				if value == "" {
					p.notice = "API key is required"
					return m, nil
				}
				a.request.Secret = value
				return m, m.executeSettingAction()
			}
			select {
			case a.input <- value:
				p.input.SetValue("")
				p.input.Blur()
				a.prompt = nil
				p.notice = "Working…"
			default:
			}
			return m, a.wait
		}
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
	a.prompt = &msg.prompt
	a.choice = 0
	a.page = 0
	p.input.SetValue("")
	p.notice = ""
	if msg.prompt.AllowInput {
		p.input.EchoMode = textinput.EchoPassword
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
	p.notice = settingsActionNotice(msg.result, msg.err)
	if strings.Contains(p.notice, "\n") {
		p.editing = &interaction.SettingField{Kind: interaction.SettingInfo, Label: "Action result", Description: p.notice}
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
	var menu *interaction.CommandMenu
	title := "Working… Esc cancels"
	if a.prompt != nil {
		q := a.prompt
		title = strings.Join([]string{q.Title, q.Instructions, q.URL, q.Code}, "\n")
		menu = q.Menu
		if q.AllowInput {
			return sanitizeSingleLineText(q.Title) + "\n" + p.input.View() + "\n\n" + sanitizeMultilineText(q.Instructions) + "\n" + sanitizeSingleLineText(q.URL) + "\n" + sanitizeSingleLineText(q.Code)
		}
	}
	if len(a.menus) > 0 {
		menu = a.menus[len(a.menus)-1]
	}
	if menu == nil {
		return sanitizeMultilineText(title)
	}
	layout := p.actionMenuLayout(menu)
	rows := layout.header
	if layout.more {
		return strings.Join(append(rows, ansi.Truncate("Enter: more · PgUp: back", max(1, p.layout.inner), "…")), "\n")
	}
	for i := layout.start; i < layout.end; i++ {
		v := menu.Options[i]
		prefix := "  "
		if i == a.choice {
			prefix = "› "
		}
		rows = append(rows, ansi.Truncate(sanitizeSingleLineText(fmt.Sprint(prefix, v.Label, "  ", v.Description)), max(1, p.layout.inner), "…"))
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
func (p *settingsPanel) actionMenuLayout(menu *interaction.CommandMenu) actionMenuLayout {
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
	layout.start = max(0, a.choice-available+1)
	layout.end = min(len(menu.Options), layout.start+available)
	return layout
}
