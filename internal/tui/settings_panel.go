package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type settingsPanel struct {
	usage                  bool
	action                 *settingsAction
	continuation           *interaction.TaskContinuation
	collection             *collectionDraft
	generation             uint64
	snapshot               interaction.SettingsSnapshot
	loading, saving        bool
	cancelRead             context.CancelFunc
	tab, selection, offset int
	positions              map[int]int
	search                 bool
	input                  textinput.Model
	editing                *interaction.SettingField
	choice                 int
	confirmUnset           bool
	detailOffset           int
	focusField             string
	notice                 string
	layout                 modalLayout
	pointer                *tea.Mouse
	pressed                string
	pressedLayout          modalLayout
}

func (m model) settingsVisible() bool {
	return m.settings != nil && m.guardPending == nil && m.question == nil && m.authInput == nil && m.secretInput == nil && m.commandMenu == nil
}

func (m model) openSettings() (model, tea.Cmd, bool) {
	if m.readSettings == nil {
		return m, nil, true
	}
	return m.openSettingsPanel(false)
}

func (m model) openSettingsPanel(usage bool) (model, tea.Cmd, bool) {
	m.settingsGeneration++
	input := textinput.New()
	input.Prompt = "› "
	input.CharLimit = 0
	input.SetVirtualCursor(false)
	m.settings = &settingsPanel{generation: m.settingsGeneration, input: input, positions: m.settingsPositions, tab: m.settingsTab, usage: usage}
	if m.settings.positions == nil {
		m.settings.positions = map[int]int{}
	}
	m.settings.selection = m.settings.positions[m.settings.tab]
	m.resizeSettings()
	return m, m.refreshSettings(), true
}

func (m *model) refreshSettings() tea.Cmd {
	p := m.settings
	if p == nil {
		return nil
	}
	if p.cancelRead != nil {
		p.cancelRead()
	}
	m.settingsGeneration++
	p.generation = m.settingsGeneration
	p.continuation = nil
	p.loading = true
	read := m.readSettings
	if p.usage {
		read = m.readUsage
	}
	if read == nil {
		return nil
	}
	command, cancel := read(p.generation)
	p.cancelRead = cancel
	return command
}

func (m *model) closeSettings() {
	if p := m.settings; p != nil {
		if p.cancelRead != nil {
			p.cancelRead()
		}
		if p.action != nil {
			if p.action.cancel != nil {
				p.action.cancel()
			}
			p.action.request.Secret = ""
		}
		if !p.usage {
			m.settingsTab = p.tab
			p.positions[p.tab] = p.selection
			m.settingsPositions = p.positions
		}
		p.input.SetValue("")
	}
	m.settings = nil
	m.settingsGeneration++
}

func (m *model) resizeSettings() {
	if p := m.settings; p != nil {
		p.layout = centeredModal(m.width, m.height, 112, 36)
		p.input.SetWidth(max(1, p.layout.inner-3))
		p.pressed = ""
		if p.action != nil {
			p.action.page = 0
		}
	}
}

func (p *settingsPanel) fields() []interaction.SettingField {
	query := strings.ToLower(strings.TrimSpace(p.input.Value()))
	if !p.search {
		query = ""
	}
	category := ""
	if p.tab < len(p.snapshot.Categories) {
		category = p.snapshot.Categories[p.tab].ID
	}
	var fields []interaction.SettingField
	for _, field := range p.snapshot.Fields {
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{field.ID, field.Label, field.Description, strings.Join(field.Keywords, " ")}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		} else if field.Category != category {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func (m model) applySettingsRead(message settingsReadResult) (tea.Model, tea.Cmd) {
	p := m.settings
	if p == nil || p.generation != message.generation {
		return m, nil
	}
	p.loading = false
	p.cancelRead = nil
	if message.err != nil {
		p.notice = message.err.Error()
		return m, nil
	}
	p.snapshot = message.snapshot
	if p.focusField != "" {
		for _, field := range p.snapshot.Fields {
			if field.ID != p.focusField {
				continue
			}
			for i, category := range p.snapshot.Categories {
				if category.ID == field.Category {
					p.tab = i
				}
			}
			for i, candidate := range p.fields() {
				if candidate.ID == field.ID {
					p.selection = i
				}
			}
			break
		}
		p.focusField = ""
	}
	p.tab = min(p.tab, max(0, len(p.snapshot.Categories)-1))
	p.selection = min(p.selection, max(0, len(p.fields())-1))
	return m, nil
}

func (m model) applySettingsSave(message settingsSaveResult) (tea.Model, tea.Cmd) {
	// Closing a window invalidates presentation only. The controller always
	// finishes publication; reopen reads the application's committed revision.
	if message.snapshot.Runtime.Model.ID != "" {
		m.currentModel = message.snapshot.Runtime.Model
		m.thinking = message.snapshot.Runtime.Thinking
		m.apiKeyConfigured = message.snapshot.Runtime.APIKeyConfigured
		m.contextUsage = message.snapshot.Runtime.Context
	}
	p := m.settings
	if p == nil || p.generation != message.generation {
		return m, nil
	}
	p.saving = false
	if message.snapshot.Categories != nil {
		p.snapshot = message.snapshot
	}
	if message.err != nil {
		p.notice = message.err.Error()
		if p.editing != nil && !p.confirmUnset && p.collection == nil && (p.editing.Kind != interaction.SettingEnum || p.editing.AllowCustom) {
			return m, p.input.Focus()
		}
		return m, nil
	}
	p.editing = nil
	p.search = false
	p.collection = nil
	p.confirmUnset = false
	p.input.SetValue("")
	p.input.Blur()
	p.notice = "Saved to user settings · " + string(message.result.Applies)
	if len(message.result.Warnings) > 0 {
		p.notice += " · " + strings.Join(message.result.Warnings, "; ")
	}
	return m, nil
}

func (m *model) submitSetting(change interaction.SettingChange) tea.Cmd {
	return m.submitSettings([]interaction.SettingChange{change})
}
func (m *model) submitSettings(changes []interaction.SettingChange) tea.Cmd {
	p := m.settings
	if p == nil || p.saving || m.writeSettings == nil {
		return nil
	}
	m.invalidateSettingsRead()
	p.continuation = nil
	p.saving = true
	p.notice = "Saving…"
	p.input.Blur()
	return m.writeSettings(p.generation, interaction.SettingsRequest{Revision: p.snapshot.Revision, Changes: changes})
}

func settingValueText(value interaction.SettingValue, invert bool) string {
	switch value.Kind {
	case interaction.SettingBool:
		enabled := value.Bool
		if invert {
			enabled = !enabled
		}
		if enabled {
			return "On"
		}
		return "Off"
	case interaction.SettingInt:
		return strconv.FormatInt(value.Int, 10)
	case interaction.SettingDuration:
		return value.Duration.String()
	case interaction.SettingContexts:
		return fmt.Sprintf("%d overrides", len(value.Contexts))
	case interaction.SettingList:
		return strings.Join(value.List, ", ")
	default:
		if value.Text == "" {
			return "(default)"
		}
		return value.Text
	}
}

func (m model) beginSettingEdit(unset bool) (tea.Model, tea.Cmd) {
	p := m.settings
	p.continuation = nil
	fields := p.fields()
	if len(fields) == 0 || p.saving {
		return m, nil
	}
	field := fields[min(p.selection, len(fields)-1)]
	if field.DisabledReason != "" {
		p.notice = field.DisabledReason
		return m, nil
	}
	if field.Kind == interaction.SettingInfo {
		p.editing = &field
		p.search = false
		p.input.Blur()
		p.detailOffset = 0
		return m, nil
	}
	if unset {
		if field.Inherited == nil {
			p.notice = field.InheritanceError
			return m, nil
		}
		p.editing = &field
		p.confirmUnset = true
		p.notice = "Remove user override? Enter confirms · Esc cancels"
		p.input.Blur()
		return m, nil
	}
	if field.Kind == interaction.SettingBool {
		if !field.Value.Bool && field.Action != nil {
			return m.openSettingAction(field)
		}
		value := field.Value
		value.Bool = !value.Bool
		return m, m.submitSetting(interaction.SettingChange{ID: field.ID, Value: value})
	}
	if field.Kind == interaction.SettingAction {
		return m.openSettingAction(field)
	}
	if field.Kind == interaction.SettingContexts || field.Kind == interaction.SettingList {
		p.editing = &field
		p.search = false
		p.input.Blur()
		p.input.SetValue("")
		value := field.Value
		value.Contexts = slices.Clone(value.Contexts)
		value.List = slices.Clone(value.List)
		p.collection = &collectionDraft{value: value}
		p.notice = ""
		return m, nil
	}
	p.editing = &field
	p.confirmUnset = false
	p.search = false
	p.notice = ""
	p.choice = 0
	for i, choice := range field.Choices {
		if choice.Value == field.Value.Text {
			p.choice = i
		}
	}
	p.input.SetValue(field.Value.Text)
	if field.Kind == interaction.SettingInt {
		p.input.SetValue(strconv.FormatInt(field.Value.Int, 10))
	}
	if field.Kind == interaction.SettingDuration {
		p.input.SetValue(field.Value.Duration.String())
	}
	if field.Kind == interaction.SettingEnum && !field.AllowCustom {
		p.input.Blur()
		return m, nil
	}
	p.input.CursorEnd()
	return m, p.input.Focus()
}

func (m model) handleSettings(message tea.Msg) (tea.Model, tea.Cmd) {
	p := m.settings
	if p == nil {
		return m, nil
	}
	if key, ok := message.(tea.KeyPressMsg); ok {
		name := key.String()
		if name == "f6" && !p.usage && m.running {
			m.requestRunCancellation()
			return m, nil
		}
		if name == "f6" && m.canContinueTask() {
			return m.continueTask()
		}
		if p.action != nil {
			return m.settingActionKey(key)
		}
		if p.collection != nil && !p.saving {
			return m.collectionKey(key)
		}
		if name == "esc" {
			if p.saving {
				m.closeSettings()
				return m, nil
			}
			if p.editing != nil {
				p.continuation = nil
				p.editing = nil
				p.confirmUnset = false
				p.input.SetValue("")
				p.input.Blur()
				p.notice = ""
				return m, nil
			}
			if p.search {
				p.search = false
				p.input.SetValue("")
				p.input.Blur()
				p.selection = p.positions[p.tab]
				return m, nil
			}
			m.closeSettings()
			return m, nil
		}
		if p.saving {
			return m, nil
		}
		if p.editing != nil {
			field := p.editing
			if field.Kind == interaction.SettingInfo {
				switch name {
				case "up":
					p.detailOffset = max(0, p.detailOffset-1)
				case "down":
					p.detailOffset++
				case "pgdown":
					p.detailOffset += p.layout.bodyHeight
				case "pgup":
					p.detailOffset = max(0, p.detailOffset-p.layout.bodyHeight)
				}
				return m, nil
			}
			if p.confirmUnset {
				if name == "enter" {
					changes := []interaction.SettingChange{{ID: field.ID, Unset: true}}
					if len(field.ResetIDs) > 0 {
						changes = nil
						for _, id := range field.ResetIDs {
							changes = append(changes, interaction.SettingChange{ID: id, Unset: true})
						}
					}
					return m, m.submitSettings(changes)
				}
				return m, nil
			}
			if field.Kind == interaction.SettingEnum && !field.AllowCustom {
				switch name {
				case "up":
					p.choice = max(0, p.choice-1)
				case "down":
					p.choice = min(max(0, len(field.Choices)-1), p.choice+1)
				case "enter":
					if len(field.Choices) > 0 {
						return m, m.submitSetting(interaction.SettingChange{ID: field.ID, Value: interaction.SettingValue{Kind: field.Kind, Text: field.Choices[p.choice].Value}})
					}
				}
				return m, nil
			}
			if name == "enter" {
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
			}
		} else {
			switch name {
			case "tab", "shift+tab", "ctrl+right", "ctrl+left":
				delta := 1
				if name == "shift+tab" || name == "ctrl+left" {
					delta = -1
				}
				n := len(p.snapshot.Categories)
				if n > 0 {
					p.positions[p.tab] = p.selection
					p.tab = (p.tab + delta + n) % n
					p.selection = p.positions[p.tab]
					p.offset = 0
				}
				return m, nil
			case "up":
				p.selection = max(0, p.selection-1)
				return m, nil
			case "down":
				p.selection = min(max(0, len(p.fields())-1), p.selection+1)
				return m, nil
			case "enter":
				return m.beginSettingEdit(false)
			case " ":
				if !p.search {
					return m.beginSettingEdit(false)
				}
			case "?":
				if !p.search {
					fields := p.fields()
					if len(fields) > 0 {
						f := fields[min(p.selection, len(fields)-1)]
						f.Description = settingDetails(f, p.snapshot.SavePath)
						f.Kind = interaction.SettingInfo
						p.editing = &f
						p.detailOffset = 0
						p.input.Blur()
					}
					return m, nil
				}
			case "D":
				if !p.search {
					fields := p.fields()
					if len(fields) > 0 {
						f := fields[min(p.selection, len(fields)-1)]
						if f.Default != nil && f.DisabledReason == "" {
							if len(f.DefaultChanges) > 0 {
								return m, m.submitSettings(f.DefaultChanges)
							}
							return m, m.submitSetting(interaction.SettingChange{ID: f.ID, Value: *f.Default})
						}
					}
					return m, nil
				}
			case "u":
				if !p.search {
					return m.beginSettingEdit(true)
				}
			case "r":
				if !p.search {
					return m, m.refreshSettings()
				}
			case "/":
				if !p.search {
					p.search = true
					p.notice = ""
					p.positions[p.tab] = p.selection
					p.selection = 0
					p.input.SetValue("")
					return m, p.input.Focus()
				}
			}
		}
	}
	if p.input.Focused() {
		var command tea.Cmd
		p.input, command = p.input.Update(message)
		if p.search {
			p.selection = 0
		}
		return m, m.scopeInputCommand(command)
	}
	return m, nil
}

func (m *model) invalidateSettingsRead() {
	p := m.settings
	if p == nil {
		return
	}
	if p.cancelRead != nil {
		p.cancelRead()
		p.cancelRead = nil
	}
	p.loading = false
	m.settingsGeneration++
	p.generation = m.settingsGeneration
}
