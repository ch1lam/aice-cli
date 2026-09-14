package tui

import (
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// syncCommandCompletion derives the first choice level from the draft. Deeper
// levels retain their explicit selection path until the command name changes.
func (m *model) syncCommandCompletion() {
	if m.running || m.side.isVisible || m.secretInput != nil || m.authInput != nil ||
		m.guardPending != nil || m.commandDismissed || len(m.pastes) > 0 {
		m.commandMenu = nil
		return
	}
	value := strings.TrimLeft(m.input.Value(), " \t")
	request, slash := parseSlashCommand(value)
	if !slash || strings.ContainsAny(value, "\r\n") || !strings.ContainsAny(value, " \t") {
		m.commandMenu = nil
		return
	}
	command, exists := findSlashCommand(m.commands, request.Name)
	if !exists || command.Menu == nil {
		m.commandMenu = nil
		return
	}
	if m.commandMenu != nil && m.commandMenu.command.Name == command.Name {
		return
	}
	m.commandMenu = &commandMenuState{
		raw:     "/" + command.Name,
		request: SlashCommandRequest{Name: command.Name},
		command: command,
		frames:  []commandMenuFrame{{menu: *command.Menu}},
	}
	m.resetCommandOptionSelection()
}

func (m *model) resetCommandOptionSelection() {
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	frame.selection = 0
	request, _ := parseSlashCommand(m.input.Value())
	if request.Arguments == "" {
		frame.selection = currentSlashCommandOption(m.matchingCommandOptions())
	}
}

func (m model) matchingCommandOptions() []SlashCommandOption {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return nil
	}
	request, _ := parseSlashCommand(m.input.Value())
	frame := m.commandMenu.frames[len(m.commandMenu.frames)-1]
	type match struct {
		option SlashCommandOption
		score  int
	}
	var ranked []match
	for _, option := range frame.menu.Options {
		labelScore, _ := fuzzyMatch(option.Label, request.Arguments)
		valueScore, _ := fuzzyMatch(option.Arguments, request.Arguments)
		if score := max(labelScore, valueScore); score >= 0 {
			ranked = append(ranked, match{option: option, score: score})
		}
	}
	slices.SortStableFunc(ranked, func(a, b match) int { return b.score - a.score })
	matches := make([]SlashCommandOption, len(ranked))
	for index, hit := range ranked {
		matches[index] = hit.option
	}
	return matches
}

func (m model) completeCommandMenuOption() (model, tea.Cmd, bool) {
	options := m.matchingCommandOptions()
	if len(options) == 0 {
		return m, nil, true
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	option := options[min(max(frame.selection, 0), len(options)-1)]
	if option.Menu != nil {
		return m.selectCommandMenuOption()
	}
	m.input.SetValue("/" + m.commandMenu.command.Name + " " + option.Arguments)
	m.input.CursorEnd()
	// Different actions can share arguments (e.g. reuse or replace a key).
	// Keep the selected action when completion changes the filtered list.
	for index, candidate := range m.matchingCommandOptions() {
		if candidate == option {
			frame.selection = index
			break
		}
	}
	return m.settleCommand(false, nil)
}

func (m model) commandArgumentHint() string {
	if m.running || m.side.isVisible || m.secretInput != nil || m.authInput != nil ||
		m.commandDismissed || len(m.pastes) > 0 || strings.ContainsAny(m.input.Value(), "\r\n") {
		return ""
	}
	request, slash := parseSlashCommand(m.input.Value())
	if !slash || request.Arguments != "" {
		return ""
	}
	command, exists := findSlashCommand(m.commands, request.Name)
	if !exists {
		return ""
	}
	hint := command.ArgumentHint
	menu := command.Menu
	if m.commandMenu != nil && len(m.commandMenu.frames) > 1 {
		menu = &m.commandMenu.frames[len(m.commandMenu.frames)-1].menu
		hint = ""
	}
	if hint == "" && menu != nil {
		hint = "<" + strings.ToLower(strings.TrimPrefix(menu.Title, "Select ")) + ">"
	}
	if hint != "" && !strings.HasSuffix(m.input.Value(), " ") && !strings.HasSuffix(m.input.Value(), "\t") {
		hint = " " + hint
	}
	return sanitizeToolDetail(hint, false)
}

// Paint the hint over empty cells only. It never enters the textarea value,
// changes its height, or moves the real terminal cursor / IME anchor.
func (m model) commandInputView(width int) string {
	view := m.input.View()
	hint := m.commandArgumentHint()
	if hint == "" || m.input.Column() != utf8.RuneCountInString(m.input.Value()) {
		return view
	}
	cursor := m.input.Cursor()
	if cursor == nil {
		return view
	}
	x, y := cursor.X, cursor.Y
	lines := strings.Split(view, "\n")
	if y < 0 || y >= len(lines) || x >= width {
		return view
	}
	hint = ansi.Truncate(hint, width-x, "")
	lines[y] = ansi.Cut(lines[y], 0, x) + mutedStyle.Render(hint) +
		ansi.Cut(lines[y], x+ansi.StringWidth(hint), width)
	return strings.Join(lines, "\n")
}
