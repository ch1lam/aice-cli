package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Settings actions. Each mode offers the ones it serves with its own labels;
// handleSettingsAction applies them by mode, for keys and clicks alike.
const (
	inputActionSettingsBack     inputAction = "settings.back"
	inputActionSettingsAccept   inputAction = "settings.accept"
	inputActionSettingsMove     inputAction = "settings.move"
	inputActionSettingsPage     inputAction = "settings.page"
	inputActionSettingsTab      inputAction = "settings.tab"
	inputActionSettingsFold     inputAction = "settings.fold"
	inputActionSettingsDetails  inputAction = "settings.details"
	inputActionSettingsDefault  inputAction = "settings.default"
	inputActionSettingsInherit  inputAction = "settings.inherit"
	inputActionSettingsRefresh  inputAction = "settings.refresh"
	inputActionSettingsSearch   inputAction = "settings.search"
	inputActionSettingsStopRun  inputAction = "settings.stop-run"
	inputActionSettingsSave     inputAction = "settings.save"
	inputActionSettingsAdd      inputAction = "settings.add"
	inputActionSettingsDelete   inputAction = "settings.delete"
	inputActionSettingsReorder  inputAction = "settings.reorder"
	inputActionSettingsCell     inputAction = "settings.cell"
	inputActionSettingsDiscard  inputAction = "settings.discard"
	inputActionSettingsKeepList inputAction = "settings.keep"
)

func directionBindings(action inputAction, label, description, back, forward string) []inputBinding {
	previous := inputBinding{action: action, binding: key.NewBinding(key.WithKeys(back), key.WithHelp(label, description)), argument: -1, short: true}
	next := inputBinding{action: action, binding: key.NewBinding(key.WithKeys(forward), key.WithHelp(label, description)), argument: 1, short: true}
	return []inputBinding{previous, next}
}

// silent binds keys without advertising them.
func silent(action inputAction, argument int, keys ...string) inputBinding {
	return inputBinding{action: action, binding: key.NewBinding(key.WithKeys(keys...)), argument: argument}
}

func (m model) settingsInputBindings() []inputBinding {
	p := m.settings
	var bindings []inputBinding
	// Stopping the run is the urgent control while one runs; letters type
	// whenever an editor has the keyboard.
	if m.run.running() && !p.usage && !m.run.cancelRequested && !p.input.Focused() {
		bindings = append(bindings, actionBinding(inputActionSettingsStopRun, "x", "stop run", "x"))
	}
	if p.saving {
		return append(bindings, actionBinding(inputActionSettingsBack, "Esc", "close", "esc"))
	}
	switch p.mode() {
	case settingsModeAction:
		return append(bindings, m.settingsActionBindings()...)
	case settingsModeCollection:
		return append(bindings, p.collectionBindings()...)
	case settingsModeInfo:
		bindings = append(bindings, actionBinding(inputActionSettingsBack, "Esc", "back", "esc"))
		bindings = append(bindings, directionBindings(inputActionSettingsMove, "↑↓", "scroll", "up", "down")...)
		return append(bindings, silent(inputActionSettingsPage, -1, "pgup"), silent(inputActionSettingsPage, 1, "pgdown"))
	case settingsModeConfirmUnset:
		return append(bindings,
			actionBinding(inputActionSettingsAccept, "Enter", "confirm", "enter"),
			actionBinding(inputActionSettingsBack, "Esc", "cancel", "esc"),
		)
	case settingsModeChoice:
		bindings = append(bindings,
			actionBinding(inputActionSettingsAccept, "Enter", "save", "enter"),
			actionBinding(inputActionSettingsBack, "Esc", "cancel", "esc"),
		)
		return append(bindings, directionBindings(inputActionSettingsMove, "↑↓", "choose", "up", "down")...)
	case settingsModeText:
		return append(bindings,
			actionBinding(inputActionSettingsAccept, "Enter", "save", "enter"),
			actionBinding(inputActionSettingsBack, "Esc", "cancel", "esc"),
		)
	}
	// Browsing and searching the fields, or the usage pages.
	enter, back := "edit", "close"
	switch {
	case p.usage:
		enter = "details"
	case p.search:
		back = "end search"
	}
	bindings = append(bindings,
		actionBinding(inputActionSettingsAccept, "Enter", enter, "enter"),
		actionBinding(inputActionSettingsBack, "Esc", back, "esc"),
	)
	category := "category"
	if p.usage {
		category = "page"
	}
	tabs := directionBindings(inputActionSettingsTab, "Tab", category, "shift+tab", "tab")
	tabs[0].short = false
	bindings = append(bindings, tabs[1], tabs[0])
	bindings = append(bindings, directionBindings(inputActionSettingsMove, "↑↓", "select", "up", "down")...)
	if p.search {
		return bindings
	}
	bindings = append(bindings, silent(inputActionSettingsAccept, 0, "space"), silent(inputActionSettingsAccept, 0, " "))
	folds := directionBindings(inputActionSettingsFold, "←→", "fold", "left", "right")
	folds[0].short, folds[1].short = false, false
	bindings = append(bindings, folds...)
	if p.usage {
		return append(bindings, actionBinding(inputActionSettingsRefresh, "r", "refresh", "r"))
	}
	return append(bindings,
		actionBinding(inputActionSettingsDetails, "?", "details", "?"),
		actionBinding(inputActionSettingsDefault, "D", "default", "D"),
		actionBinding(inputActionSettingsInherit, "u", "inherit", "u"),
		actionBinding(inputActionSettingsSearch, "/", "search", "/"),
		inputBinding{action: inputActionSettingsRefresh, binding: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))},
	)
}

func (p *settingsPanel) collectionBindings() []inputBinding {
	d := p.collection
	switch {
	case d.leaving:
		return []inputBinding{
			actionBinding(inputActionSettingsSave, "s", "save", "s"),
			actionBinding(inputActionSettingsDiscard, "d", "discard", "d"),
			actionBinding(inputActionSettingsKeepList, "k/Esc", "keep editing", "k", "esc"),
		}
	case d.editing:
		cells := directionBindings(inputActionSettingsCell, "Tab", "next cell", "shift+tab", "tab")
		cells[0].short = false
		bindings := []inputBinding{
			actionBinding(inputActionSettingsAccept, "Enter", "keep row", "enter"),
			actionBinding(inputActionSettingsBack, "Esc", "cancel row", "esc"),
		}
		if len(d.cells) > 1 {
			bindings = append(bindings, cells[1], cells[0])
		}
		return bindings
	}
	enter := "edit"
	if d.count() == 0 {
		enter = "add"
	}
	reorder := directionBindings(inputActionSettingsReorder, "Ctrl+↑↓", "move", "ctrl+up", "ctrl+down")
	bindings := []inputBinding{
		actionBinding(inputActionSettingsSave, "Ctrl+s", "save list", "ctrl+s"),
		actionBinding(inputActionSettingsBack, "Esc", "back", "esc"),
		actionBinding(inputActionSettingsAccept, "Enter", enter, "enter"),
		actionBinding(inputActionSettingsAdd, "a", "add", "a"),
		actionBinding(inputActionSettingsDelete, "d", "delete", "d", "delete"),
	}
	bindings = append(bindings, reorder...)
	return append(bindings, directionBindings(inputActionSettingsMove, "↑↓", "select", "up", "down")...)
}

func (m model) settingsActionBindings() []inputBinding {
	p := m.settings
	a := p.action
	back := "back"
	if a.running {
		back = "cancel"
	}
	bindings := []inputBinding{actionBinding(inputActionSettingsBack, "Esc", back, "esc")}
	if a.cancelled {
		return bindings
	}
	if menu, selection := a.activeMenu(); menu != nil {
		bindings = append(bindings, silent(inputActionSettingsPage, -1, "pgup"))
		if p.actionMenuLayout(menu, *selection).more {
			more := actionBinding(inputActionSettingsPage, "Enter", "more", "enter", "down", "pgdown")
			more.argument = 1
			return append([]inputBinding{more}, append(bindings, silent(inputActionSettingsPage, -1, "up"))...)
		}
		choose := actionBinding(inputActionSettingsAccept, "Enter", "choose", "enter")
		return append([]inputBinding{choose}, append(bindings, directionBindings(inputActionSettingsMove, "↑↓", "select", "up", "down")...)...)
	}
	if a.form != nil || a.prompt != nil && a.prompt.AllowInput {
		return append([]inputBinding{actionBinding(inputActionSettingsAccept, "Enter", "submit", "enter")}, bindings...)
	}
	return bindings
}

// handleSettingsAction applies a Settings action in the panel's mode.
func (m model) handleSettingsAction(match inputActionMatch) (tea.Model, tea.Cmd) {
	p := m.settings
	if match.action == inputActionSettingsStopRun {
		m.requestRunCancellation()
		return m, nil
	}
	if p.saving {
		m.closeSettings()
		return m, nil
	}
	switch p.mode() {
	case settingsModeAction:
		return m.settingsFlowAction(match)
	case settingsModeCollection:
		return m.collectionAction(match)
	}
	if match.action == inputActionSettingsBack {
		switch {
		case p.editing != nil:
			p.editing = nil
			p.confirmUnset = false
			p.input.SetValue("")
			p.input.Blur()
			p.notice = ""
		case p.search:
			p.search = false
			p.input.SetValue("")
			p.input.Blur()
			p.selection = p.positions[p.tab]
		default:
			m.closeSettings()
		}
		return m, nil
	}
	field := p.editing
	switch p.mode() {
	case settingsModeInfo:
		step := match.argument
		if match.action == inputActionSettingsPage {
			step *= p.layout.bodyHeight
		}
		p.detailOffset = max(0, p.detailOffset+step)
	case settingsModeConfirmUnset:
		changes := []interaction.SettingChange{{ID: field.ID, Unset: true}}
		if len(field.ResetIDs) > 0 {
			changes = nil
			for _, id := range field.ResetIDs {
				changes = append(changes, interaction.SettingChange{ID: id, Unset: true})
			}
		}
		return m, m.submitSettings(changes)
	case settingsModeChoice:
		if match.action == inputActionSettingsMove {
			p.choice = clampIndex(p.choice+match.argument, len(field.Choices))
		} else if len(field.Choices) > 0 {
			return m, m.submitSetting(interaction.SettingChange{ID: field.ID, Value: interaction.SettingValue{Kind: field.Kind, Text: field.Choices[p.choice].Value}})
		}
	case settingsModeText:
		value := interaction.SettingValue{Kind: field.Kind}
		var err error
		switch field.Kind {
		case interaction.SettingInt:
			value.Int, err = strconv.ParseInt(strings.TrimSpace(p.input.Value()), 10, 64)
		case interaction.SettingDuration:
			value.Duration, err = time.ParseDuration(strings.TrimSpace(p.input.Value()))
		default:
			value.Text = p.input.Value()
		}
		if err != nil {
			p.notice = err.Error()
			return m, nil
		}
		return m, m.submitSetting(interaction.SettingChange{ID: field.ID, Value: value})
	default:
		return m.browseSettingsAction(match)
	}
	return m, nil
}

func (m model) browseSettingsAction(match inputActionMatch) (tea.Model, tea.Cmd) {
	p := m.settings
	fields := p.fields()
	selected := func() (interaction.SettingField, bool) {
		if len(fields) == 0 || p.selectedSectionCollapsed(fields) {
			return interaction.SettingField{}, false
		}
		return fields[min(p.selection, len(fields)-1)], true
	}
	switch match.action {
	case inputActionSettingsTab:
		if n := len(p.snapshot.Categories); n > 0 {
			p.positions[p.tab] = p.selection
			p.tab = wrapIndex(p.tab, match.argument, n)
			p.selection = p.positions[p.tab]
		}
	case inputActionSettingsMove:
		p.moveSettingSelection(match.argument)
	case inputActionSettingsFold:
		if len(fields) > 0 {
			p.setSectionCollapsed(settingSectionKey(fields[min(p.selection, len(fields)-1)]), match.argument < 0)
		}
	case inputActionSettingsAccept:
		return m.beginSettingEdit(false)
	case inputActionSettingsInherit:
		return m.beginSettingEdit(true)
	case inputActionSettingsDetails:
		if f, ok := selected(); ok {
			f.Description = settingDetails(f, p.snapshot.SavePath)
			f.Kind = interaction.SettingInfo
			p.editing = &f
			p.detailOffset = 0
			p.input.Blur()
		}
	case inputActionSettingsDefault:
		if f, ok := selected(); ok && f.Default != nil && f.DisabledReason == "" {
			if len(f.DefaultChanges) > 0 {
				return m, m.submitSettings(f.DefaultChanges)
			}
			return m, m.submitSetting(interaction.SettingChange{ID: f.ID, Value: *f.Default})
		}
	case inputActionSettingsRefresh:
		return m, m.refreshSettings()
	case inputActionSettingsSearch:
		p.search = true
		p.searchCollapsed = nil
		p.notice = ""
		p.positions[p.tab] = p.selection
		p.selection = 0
		p.input.SetValue("")
		return m, p.input.Focus()
	}
	return m, nil
}

func (m model) collectionAction(match inputActionMatch) (tea.Model, tea.Cmd) {
	p := m.settings
	d := p.collection
	switch match.action {
	case inputActionSettingsKeepList:
		d.leaving = false
		p.notice = "Draft retained"
	case inputActionSettingsDiscard:
		p.collection = nil
		p.editing = nil
		p.input.SetValue("")
		p.input.Blur()
		p.notice = "Draft discarded"
	case inputActionSettingsSave:
		d.leaving = false
		return m, m.submitSetting(interaction.SettingChange{ID: p.editing.ID, Value: d.value})
	case inputActionSettingsCell:
		d.cells[d.cell] = p.input.Value()
		d.cell = wrapIndex(d.cell, match.argument, len(d.cells))
		p.input.SetValue(d.cells[d.cell])
		p.input.CursorEnd()
	case inputActionSettingsBack:
		switch {
		case d.editing:
			d.editing = false
			p.input.Blur()
			p.input.SetValue("")
		case d.dirty:
			d.leaving = true
			p.notice = "Unsaved list"
		default:
			p.collection = nil
			p.editing = nil
		}
	case inputActionSettingsAccept:
		if d.editing {
			return m.keepCollectionRow()
		}
		return m.editCollectionRow(d.count() == 0)
	case inputActionSettingsAdd:
		return m.editCollectionRow(true)
	case inputActionSettingsMove:
		d.row = clampIndex(d.row+match.argument, d.count())
	case inputActionSettingsReorder:
		other := d.row + match.argument
		if other >= 0 && other < d.count() {
			if d.value.Kind == interaction.SettingList {
				d.value.List[d.row], d.value.List[other] = d.value.List[other], d.value.List[d.row]
			} else {
				d.value.Contexts[d.row], d.value.Contexts[other] = d.value.Contexts[other], d.value.Contexts[d.row]
			}
			d.row = other
			d.dirty = true
		}
	case inputActionSettingsDelete:
		if d.count() > 0 {
			if d.value.Kind == interaction.SettingList {
				d.value.List = slices.Delete(d.value.List, d.row, d.row+1)
			} else {
				d.value.Contexts = slices.Delete(d.value.Contexts, d.row, d.row+1)
			}
			d.row = min(d.row, max(0, d.count()-1))
			d.dirty = true
		}
	}
	return m, nil
}

// editCollectionRow opens the row editor on a new row or the selected one.
func (m model) editCollectionRow(adding bool) (tea.Model, tea.Cmd) {
	p := m.settings
	d := p.collection
	d.adding = adding
	d.cell = 0
	d.cells = []string{""}
	if d.value.Kind == interaction.SettingContexts {
		d.cells = []string{"", "", ""}
		if !adding {
			v := d.value.Contexts[d.row]
			d.cells = []string{v.Provider, v.Model, strconv.FormatInt(v.Tokens, 10)}
		}
	} else if !adding {
		d.cells[0] = d.value.List[d.row]
	}
	d.editing = true
	p.input.SetValue(d.cells[0])
	p.notice = ""
	return m, p.input.Focus()
}

// keepCollectionRow writes the edited row into the draft.
func (m model) keepCollectionRow() (tea.Model, tea.Cmd) {
	p := m.settings
	d := p.collection
	d.cells[d.cell] = p.input.Value()
	if d.value.Kind == interaction.SettingContexts {
		tokens, err := strconv.ParseInt(strings.TrimSpace(d.cells[2]), 10, 64)
		if err != nil || tokens <= 0 {
			p.notice = "Tokens must be a positive integer"
			return m, nil
		}
		entry := interaction.ContextWindowSetting{Provider: strings.TrimSpace(d.cells[0]), Model: strings.TrimSpace(d.cells[1]), Tokens: tokens}
		if entry.Provider == "" || entry.Model == "" {
			p.notice = "Provider and model are required"
			return m, nil
		}
		if d.adding {
			d.value.Contexts = append(d.value.Contexts, entry)
		} else {
			d.value.Contexts[d.row] = entry
		}
	} else {
		entry := strings.TrimSpace(d.cells[0])
		if entry == "" {
			p.notice = "An entry cannot be blank; delete the row to remove it"
			return m, nil
		}
		if d.adding {
			d.value.List = append(d.value.List, entry)
		} else {
			d.value.List[d.row] = entry
		}
	}
	d.dirty = true
	d.editing = false
	p.input.Blur()
	p.input.SetValue("")
	p.notice = "Row updated in draft"
	return m, nil
}

// settingsFlowAction drives an action form: its menus, credential steps and
// prompts, and cancelling a running action.
func (m model) settingsFlowAction(match inputActionMatch) (tea.Model, tea.Cmd) {
	p := m.settings
	a := p.action
	if match.action == inputActionSettingsBack {
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
		switch match.action {
		case inputActionSettingsPage:
			a.page = max(0, a.page+match.argument)
		case inputActionSettingsMove:
			*selection = clampIndex(*selection+match.argument, len(menu.Options))
		case inputActionSettingsAccept:
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
	if match.action != inputActionSettingsAccept {
		return m, nil
	}
	if a.form != nil {
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
	if a.prompt != nil && a.prompt.AllowInput {
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
	return m, nil
}
