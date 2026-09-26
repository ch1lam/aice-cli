package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
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

	prompt := strings.TrimSpace(m.expandComposerText())
	if prompt == "" && len(m.composerImages()) == 0 {
		return m, nil, true
	}
	// Pasted placeholders are literal content, never a slash command, even
	// when the expanded text alone would parse as one.
	if len(m.pastes) == len(m.composerImages()) && len(m.input.files) == 0 {
		if request, slashCommand := parseSlashCommand(prompt); slashCommand {
			if len(m.composerImages()) > 0 {
				m.inputNotice = "Send or remove attached images before running a slash command"
				return m.settleCommand(false, nil)
			}
			// Side questions never enter the main prompt history, even when
			// submitted through the main composer while a run is active.
			if request.Name != "btw" && request.Name != "settings" && request.Name != "desktop" && request.Name != "usage" && request.Name != "context" && request.Name != "session" {
				m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
				m.historyIndex = -1
				m.historyDraft = ""
			}
			return m.submitSlashCommand(prompt, request)
		}
	}
	m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
	m.historyIndex = -1
	m.historyDraft = ""
	if m.controllerClosed {
		return m, nil, true
	}

	input := RunInput{Prompt: prompt, Files: m.composerFiles(), Images: interaction.CloneImages(m.composerImages())}
	m.submittedDraft = composerDraft{text: m.input.Value(), pastes: m.pastes, files: m.input.files}
	m.input.Reset()
	m.pastes = nil
	m.inputNotice = ""
	m.commandSelection = 0
	m.commandDismissed = false
	return m.beginSubmittedRun(input)
}

// Composer submission and explicit Settings continuation share run startup.
// Their callers own whether the current composer draft is consumed.
func (m model) beginSubmittedRun(input RunInput) (model, tea.Cmd, bool) {
	m.submittedInput = &input
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: imageInputText(input.Prompt, len(input.Images))})
	m.beginProcess()
	m.pendingDeliveries = nil
	m.activeRun = nil
	m.acceptsDelivery = false
	m.input.Focus()
	m.running = true
	m.assistantEntry = -1
	m.status = "Starting response..."
	return m.settleCommand(
		true,
		startRun(m.requests, m.controllerDone, input),
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
	case "desktop":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.resetCommandInput()
		if m.readSettings == nil {
			m.inputNotice = "Settings is unavailable"
			return m, nil, true
		}
		next, cmd, handled := m.openSettings()
		next.settings.focusField = "desktop_enabled"
		return next, cmd, handled
	case "usage", "context", "session":
		if request.Arguments == "" && m.readUsage != nil {
			tab := 1
			if request.Name == "context" {
				tab = 0
			}
			if request.Name == "session" {
				tab = 2
			}
			m.resetCommandInput()
			return m.openUsage(tab)
		}
	case "settings":
		if request.Arguments == "" && m.readSettings != nil {
			m.resetCommandInput()
			return m.openSettings()
		}
	case "history":
		if request.Arguments == "" && m.searchSessions != nil {
			m.resetCommandInput()
			return m.openSessionPicker()
		}
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
		m.folds = nil
		m.activeProcessID = 0
		m.resetCommandInput()
		m.status = "Visible transcript cleared; Session history is unchanged"
		// Clearing the transcript brings back the logo and a fresh tip.
		m.welcomeTip.selectNext()
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
	// Interactive commands (/browser, /web) receive their menu choice as
	// arguments and continue with their own prompts; other menu commands open
	// the option menu and select the supplied value.
	if command.Menu != nil && (request.Arguments == "" || !command.Interactive) {
		m, _, _ = m.openCommandMenu(raw, request, command)
		if request.Arguments != "" {
			m.input.SetValue(raw)
			m.input.CursorEnd()
			m.resetCommandOptionSelection()
			return m.selectCommandMenuOption()
		}
		return m, nil, true
	}
	return m.startApplicationSlashCommand(raw, request, command)
}

func (m model) startApplicationSlashCommand(
	raw string,
	request SlashCommandRequest,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	useSavedCredential := command.Name == "login" && request.UseSavedCredential
	accountLogin := command.Name == "login" && request.LoginMethod != ""
	if command.SecretPrompt != "" && !useSavedCredential && !accountLogin {
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
			m.input.Placeholder = "Custom endpoint URL (e.g. http://localhost:11434/v1, leave blank for default, input hidden)"
			m.status = "Custom endpoint URL required; leave blank for default"
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
		m.status = command.SecretPrompt + " required"
		return m.settleCommand(true, m.input.Focus())
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: raw})
	m.resetCommandInput()
	m.input.Blur()
	m.activeRun = nil
	m.acceptsDelivery = false
	m.running = true
	m.assistantEntry = -1
	if accountLogin || command.Interactive {
		m.authInput = make(chan string, 1)
		m.authCommand = command.Name
		request.Auth = &interaction.AuthInteraction{Input: m.authInput}
		m.input.Placeholder = "Starting /" + command.Name
		m.status = "Starting /" + command.Name + "..."
	} else if useSavedCredential {
		m.status = "Using saved credential..."
	} else {
		m.status = "Running /" + command.Name + "..."
	}
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
	m.input.SetValue("/" + command.Name + " ")
	m.input.CursorEnd()
	m.input.Focus()
	m.commandDismissed = false
	m.activeRun = nil
	m.acceptsDelivery = false
	m.status = command.Menu.Title
	return m.settleCommand(false, nil)
}

func (m model) selectCommandMenuOption() (model, tea.Cmd, bool) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return m, nil, true
	}
	if m.controllerClosed {
		return m.commandError(m.commandMenu.raw, "TUI run controller stopped")
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	options := m.matchingCommandOptions()
	if len(options) == 0 {
		return m.settleCommand(false, nil)
	}
	frame.selection = min(max(frame.selection, 0), len(options)-1)
	option := options[frame.selection]
	if option.Menu != nil && len(option.Menu.Options) > 0 {
		frame.draft = m.input.Value()
		m.commandMenu.frames = append(
			m.commandMenu.frames,
			commandMenuFrame{
				menu:      *option.Menu,
				selection: currentSlashCommandOption(option.Menu.Options),
			},
		)
		m.input.SetValue("/" + m.commandMenu.command.Name + " ")
		m.input.CursorEnd()
		m.status = option.Menu.Title
		return m.settleCommand(false, nil)
	}

	state := *m.commandMenu
	state.request.Arguments = option.Arguments
	state.request.UseSavedCredential = option.UseSavedCredential
	state.request.LoginMethod = option.LoginMethod
	m.commandMenu = nil
	m.promptHistory = appendPromptHistory(m.promptHistory, state.raw)
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
		m.input.SetValue(frame.draft)
		m.input.CursorEnd()
		m.status = frame.menu.Title
		return m.settleCommand(false, nil)
	}

	name := m.commandMenu.command.Name
	m.commandMenu = nil
	m.commandDismissed = true
	m.status = "/" + name + " selection cancelled"
	return m.settleCommand(false, m.input.Focus())
}

func (m *model) moveCommandMenuSelection(delta int) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	options := m.matchingCommandOptions()
	if len(options) == 0 {
		frame.selection = 0
		return
	}
	frame.selection = (frame.selection + delta + len(options)) % len(options)
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
				m.status = "Endpoint must start with http:// or https:// (or leave blank for default)"
				return m, nil, true
			}
			m.customLogin.endpoint = value
			m.customLogin.step = 1
			m.secretInput.prompt = "API key"
			m.input.Reset()
			m.input.Placeholder = "API key (leave empty for Ollama, input hidden)"
			m.status = "API key (leave empty for Ollama)"
			return m.settleCommand(true, m.input.Focus())
		case 1:
			m.customLogin.apiKey = value
			m.customLogin.step = 2
			m.secretInput.prompt = "Model name"
			m.input.Reset()
			m.input.Placeholder = "Model name (e.g. llama3.1:8b, leave blank for default, input hidden)"
			m.status = "Model name (leave blank for default)"
			return m.settleCommand(true, m.input.Focus())
		case 2:
			if value != "" && strings.ContainsAny(value, " \t\r\n") {
				m.status = "Model name must not contain whitespace"
				return m, nil, true
			}
			request := m.secretInput.request
			request.CustomEndpoint = m.customLogin.endpoint
			request.CustomModel = value
			request.Secret = m.customLogin.apiKey
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
	m.pastes = nil
	m.input.Placeholder = defaultPlaceholder
	m.secretInput = nil
	m.customLogin = nil
	m.commandMenu = nil
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""
}
