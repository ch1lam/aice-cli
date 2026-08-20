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
	if request, slashCommand := parseSlashCommand(prompt); slashCommand {
		// Side questions never enter the main prompt history, even when
		// submitted through the main composer while a run is active.
		if request.Name != "btw" {
			m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
			m.historyIndex = -1
			m.historyDraft = ""
		}
		return m.submitSlashCommand(prompt, request)
	}
	m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
	m.historyIndex = -1
	m.historyDraft = ""
	if m.controllerClosed {
		return m, nil, true
	}

	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: prompt})
	m.beginProcess()
	m.input.Reset()
	m.commandSelection = 0
	m.commandDismissed = false
	m.pendingDeliveries = nil
	m.activeRun = nil
	m.acceptsDelivery = false
	m.input.Focus()
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
	case "btw":
		m.resetCommandInput()
		return m.handleBTWCommand(request, raw, false)
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
		// Custom provider needs endpoint + API key + model in one centralized
		// /login flow. Use a 3-step hidden-input sequence instead of a single
		// API key prompt so the user can configure everything in one place.
		if command.Name == "login" && request.Arguments == "custom" {
			m.entries = append(
				m.entries,
				transcriptEntry{kind: entryUser, text: raw},
			)
			m.resetCommandInput()
			m.customLogin = &customLoginState{step: 0}
			m.secretInput = &secretInput{
				request: request,
				prompt:  "Custom endpoint URL",
			}
			m.input.Placeholder = "Custom endpoint URL (e.g. http://localhost:11434/v1, Enter for default, input hidden)"
			m.status = "Custom endpoint URL required; Enter for default, Esc cancels"
			return m.settleCommand(true, m.input.Focus())
		}
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
	m.activeRun = nil
	m.acceptsDelivery = false
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
	m.activeRun = nil
	m.acceptsDelivery = false
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
	// Custom login uses a 3-step sequence: endpoint -> API key -> model.
	// Empty is allowed for all steps (default endpoint / no key / default model).
	isCustomLogin := m.customLogin != nil && m.secretInput != nil && m.secretInput.request.Name == "login" && m.secretInput.request.Arguments == "custom"
	value := strings.TrimSpace(m.input.Value())
	if !isCustomLogin && value == "" {
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

	if isCustomLogin {
		switch m.customLogin.step {
		case 0:
			// Endpoint URL
			if value != "" && !(strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")) {
				m.status = "Endpoint must start with http:// or https:// (or Enter for default)"
				return m, nil, true
			}
			m.customLogin.endpoint = value
			m.customLogin.step = 1
			m.secretInput.prompt = "API key"
			m.input.Reset()
			m.input.Placeholder = "API key (leave empty for Ollama, input hidden)"
			m.status = "API key (leave empty for Ollama); Enter to continue, Esc cancels"
			return m.settleCommand(true, m.input.Focus())
		case 1:
			m.customLogin.apiKey = value
			m.customLogin.step = 2
			m.secretInput.prompt = "Model name"
			m.input.Reset()
			m.input.Placeholder = "Model name (e.g. llama3.1:8b, Enter for default, input hidden)"
			m.status = "Model name (Enter for default); Esc cancels"
			return m.settleCommand(true, m.input.Focus())
		case 2:
			if value != "" && strings.ContainsAny(value, " \t\r\n") {
				m.status = "Model name must not contain whitespace"
				return m, nil, true
			}
			endpoint := strings.TrimSpace(m.customLogin.endpoint)
			apiKey := m.customLogin.apiKey
			modelName := strings.TrimSpace(value)
			request := m.secretInput.request
			// Encode endpoint+model into Arguments so app/login can persist both.
			args := "custom"
			if endpoint != "" {
				args += " " + endpoint
				if modelName != "" {
					args += " " + modelName
				}
			} else if modelName != "" {
				// No endpoint but model present: use "-" placeholder so login can
				// distinguish endpoint omission from model. Login handles "-" as empty.
				args += " - " + modelName
			}
			request.Arguments = args
			request.Secret = apiKey
			m.customLogin = nil
			m.resetCommandInput()
			m.input.Blur()
			m.running = true
			m.assistantEntry = -1
			m.status = "Saving custom provider..."
			return m.settleCommand(
				true,
				startSlashCommand(m.requests, m.controllerDone, request),
			)
		}
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
	m.customLogin = nil
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
	m.customLogin = nil
	m.commandMenu = nil
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""
}
