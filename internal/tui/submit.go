package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m model) settleCommand(forceBottom bool, command tea.Cmd) (model, tea.Cmd, bool) {
	m.resizeLayout()
	m.refreshViewport(forceBottom)
	return m, command, true
}

func (m model) submit() (model, tea.Cmd, bool) {
	if m.secretInput != nil {
		return m.submitSecretInput()
	}

	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" {
		return m, nil, true
	}
	m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
	m.historyIndex = -1
	m.historyDraft = ""
	if request, slashCommand := parseSlashCommand(prompt); slashCommand {
		return m.submitSlashCommand(prompt, request)
	}
	if m.controllerClosed {
		return m, nil, true
	}

	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: prompt})
	m.beginProcess()
	m.input.Reset()
	m.commandSelection = 0
	m.commandDismissed = false
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Starting response..."
	return m.settleCommand(
		true,
		startRun(m.requests, m.controllerDone, prompt),
	)
}

func (m model) submitSlashCommand(
	raw string,
	request SlashCommandRequest,
) (model, tea.Cmd, bool) {
	command, exists := findSlashCommand(m.commands, request.Name)
	if !exists {
		return m.commandError(
			raw,
			fmt.Sprintf(
				"Unknown slash command /%s. Use /help to list commands.",
				request.Name,
			),
		)
	}

	switch command.Name {
	case "help":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.entries = append(
			m.entries,
			transcriptEntry{kind: entryUser, text: raw},
			transcriptEntry{
				kind: entryCommand,
				text: slashCommandHelp(m.commands),
			},
		)
		m.resetCommandInput()
		m.status = "Slash commands listed"
		return m.settleCommand(true, nil)
	case "clear":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.entries = nil
		m.processGroups = nil
		m.activeProcessID = 0
		m.resetCommandInput()
		m.status = "Visible transcript cleared; Session history is unchanged"
		// Clearing the transcript brings the welcome screen back, so resume
		// its animated logo.
		return m.settleCommand(true, m.welcomeAnimation.Start())
	case "quit":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.resetCommandInput()
		return m, tea.Quit, true
	}

	if m.controllerClosed {
		return m.commandError(raw, "TUI run controller stopped")
	}
	if command.Menu != nil {
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		return m.openCommandMenu(raw, request, command)
	}
	return m.startApplicationSlashCommand(raw, request, command)
}

func (m model) startApplicationSlashCommand(
	raw string,
	request SlashCommandRequest,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	if command.SecretPrompt != "" {
		m.entries = append(
			m.entries,
			transcriptEntry{kind: entryUser, text: raw},
		)
		m.resetCommandInput()
		m.secretInput = &secretInput{
			request: request,
			prompt:  command.SecretPrompt,
		}
		m.input.Placeholder = command.SecretPrompt + " (input hidden)"
		m.status = command.SecretPrompt + " required; Esc cancels"
		return m.settleCommand(true, m.input.Focus())
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: raw})
	m.resetCommandInput()
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Running /" + command.Name + "..."
	return m.settleCommand(
		true,
		startSlashCommand(m.requests, m.controllerDone, request),
	)
}

func (m model) openCommandMenu(
	raw string,
	request SlashCommandRequest,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	if command.Menu == nil || len(command.Menu.Options) == 0 {
		return m.commandError(raw, "/"+command.Name+" has no available choices")
	}

	m.commandMenu = &commandMenuState{
		raw:     raw,
		request: request,
		command: command,
		frames: []commandMenuFrame{{
			menu:      *command.Menu,
			selection: currentSlashCommandOption(command.Menu.Options),
		}},
	}
	m.input.Blur()
	m.status = command.Menu.Title + "; Esc cancels"
	return m.settleCommand(false, nil)
}

func (m model) selectCommandMenuOption() (model, tea.Cmd, bool) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return m, nil, true
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	if len(frame.menu.Options) == 0 {
		return m.backOrCancelCommandMenu()
	}
	frame.selection = min(max(frame.selection, 0), len(frame.menu.Options)-1)
	option := frame.menu.Options[frame.selection]
	if option.Menu != nil && len(option.Menu.Options) > 0 {
		m.commandMenu.frames = append(
			m.commandMenu.frames,
			commandMenuFrame{
				menu:      *option.Menu,
				selection: currentSlashCommandOption(option.Menu.Options),
			},
		)
		m.status = option.Menu.Title + "; Esc goes back"
		return m.settleCommand(false, nil)
	}

	state := *m.commandMenu
	state.request.Arguments = option.Arguments
	m.commandMenu = nil
	return m.startApplicationSlashCommand(
		state.raw,
		state.request,
		state.command,
	)
}

func (m model) backOrCancelCommandMenu() (model, tea.Cmd, bool) {
	if m.commandMenu == nil {
		return m, nil, true
	}
	if len(m.commandMenu.frames) > 1 {
		m.commandMenu.frames = m.commandMenu.frames[:len(m.commandMenu.frames)-1]
		frame := m.commandMenu.frames[len(m.commandMenu.frames)-1]
		m.status = frame.menu.Title + "; Esc cancels"
		return m.settleCommand(false, nil)
	}

	name := m.commandMenu.command.Name
	m.commandMenu = nil
	m.resetCommandInput()
	m.status = "/" + name + " selection cancelled"
	return m.settleCommand(false, m.input.Focus())
}

func (m *model) moveCommandMenuSelection(delta int) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	if len(frame.menu.Options) == 0 {
		frame.selection = 0
		return
	}
	frame.selection = (frame.selection + delta + len(frame.menu.Options)) %
		len(frame.menu.Options)
}

func currentSlashCommandOption(options []SlashCommandOption) int {
	for index, option := range options {
		if option.Current {
			return index
		}
	}
	return 0
}

func (m model) submitSecretInput() (model, tea.Cmd, bool) {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		m.status = m.secretInput.prompt + " must not be blank"
		return m, nil, true
	}
	if strings.ContainsAny(value, "\r\n") {
		m.status = m.secretInput.prompt + " must be entered on one line"
		return m, nil, true
	}
	if m.controllerClosed {
		return m.cancelSecretInput()
	}

	request := m.secretInput.request
	request.Secret = value
	m.resetCommandInput()
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Saving credential..."
	return m.settleCommand(
		true,
		startSlashCommand(m.requests, m.controllerDone, request),
	)
}

func (m model) cancelSecretInput() (model, tea.Cmd, bool) {
	prompt := m.secretInput.prompt
	m.resetCommandInput()
	m.entries = append(m.entries, transcriptEntry{
		kind: entryNotice,
		text: prompt + " entry cancelled",
	})
	m.status = prompt + " entry cancelled; run /login to try again"
	return m.settleCommand(true, m.input.Focus())
}

func (m model) commandUsageError(
	raw string,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	return m.commandError(
		raw,
		"Usage: "+slashCommandUsage(command),
	)
}

func (m model) commandError(
	raw string,
	message string,
) (model, tea.Cmd, bool) {
	m.entries = append(
		m.entries,
		transcriptEntry{kind: entryUser, text: raw},
		transcriptEntry{kind: entryError, text: message},
	)
	m.resetCommandInput()
	m.status = message
	return m.settleCommand(true, nil)
}

func (m *model) resetCommandInput() {
	m.input.Reset()
	m.input.Placeholder = defaultPlaceholder
	m.secretInput = nil
	m.commandMenu = nil
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""
}
