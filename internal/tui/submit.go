package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func (m model) settleCommand(forceBottom bool, command tea.Cmd) (model, tea.Cmd, bool) {
	m.restoreCommandDraftIfIdle()
	m.resizeLayout()
	m.refreshViewport(forceBottom)
	return m, command, true
}

func (m model) submit() (model, tea.Cmd, bool) {
	if m.secretEntry() {
		return m.submitSecretInput()
	}

	m.attachLeadingSkill()
	prompt := strings.TrimSpace(m.expandComposerText())
	if prompt == "" && len(m.composerImages()) == 0 {
		return m, nil, true
	}
	// Pasted placeholders are literal content, never a slash command, even
	// when the expanded text alone would parse as one.
	if len(m.pastes) == len(m.composerImages()) && len(m.input.files) == 0 && len(m.input.skills) == 0 {
		if request, slashCommand := parseSlashCommand(prompt); slashCommand {
			if len(m.composerImages()) > 0 {
				m.inputNotice = "Send or remove attached images before running a slash command"
				return m.settleCommand(false, nil)
			}
			// Side questions and panel navigation never enter the main prompt
			// history, even when submitted while a run is active.
			if request.Name != "btw" && !m.opensPanel(request) {
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

	input := interaction.RunInput{Prompt: prompt, Files: m.composerFiles(), Skills: m.composerSkills(), Images: interaction.CloneImages(m.composerImages())}
	m.submittedDraft = m.captureComposerDraft()
	m.input.Reset()
	m.pastes = nil
	m.inputNotice = ""
	m.commandSelection = 0
	m.commandDismissed = false
	return m.beginSubmittedRun(input)
}

// Composer submission starts a run through this shared path.
// Their callers own whether the current composer draft is consumed.
func (m model) beginSubmittedRun(input interaction.RunInput) (model, tea.Cmd, bool) {
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
	request interaction.CommandRequest,
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

	if m.opensPanel(request) {
		m.resetCommandInput()
		return m.openPanel(*command.Panel)
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
	request interaction.CommandRequest,
	command interaction.Command,
) (model, tea.Cmd, bool) {
	return m.runCommandFlow(&commandFlow{raw: raw, command: command, request: request})
}

// runCommandFlow starts a chosen command. Credentials are first collected in
// the hidden composer; interactive commands keep the flow for their prompts.
func (m model) runCommandFlow(flow *commandFlow) (model, tea.Cmd, bool) {
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: flow.raw})
	m.resetCommandInput()
	if flow.needsCredential() {
		flow.beginCredential()
		m.flow = flow
		m.showCredentialStep()
		return m.settleCommand(true, m.input.Focus())
	}
	m.input.Blur()
	m.activeRun = nil
	m.acceptsDelivery = false
	m.running = true
	m.assistantEntry = -1
	request, name := flow.request, flow.command.Name
	switch {
	case request.LoginMethod != "" || flow.command.Interactive:
		flow.input = make(chan string, 1)
		request.Auth = &interaction.AuthInteraction{Input: flow.input}
		m.flow = flow
		m.input.Placeholder = "Starting /" + name
		m.status = "Starting /" + name + "..."
	case request.UseSavedCredential:
		m.status = "Using saved credential..."
	default:
		m.status = "Running /" + name + "..."
	}
	return m.settleCommand(true, startSlashCommand(m.requests, m.controllerDone, request))
}

func (m model) openCommandMenu(
	raw string,
	request interaction.CommandRequest,
	command interaction.Command,
) (model, tea.Cmd, bool) {
	if command.Menu == nil || len(command.Menu.Options) == 0 {
		return m.commandError(raw, "/"+command.Name+" has no available choices")
	}

	m.flow = newCommandFlow(raw, command, request)
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
	if !m.commandMenuOpen() {
		return m, nil, true
	}
	flow := m.flow
	if m.controllerClosed {
		return m.commandError(flow.raw, "TUI run controller stopped")
	}
	frame := flow.menu()
	options := m.matchingCommandOptions()
	if len(options) == 0 {
		return m.settleCommand(false, nil)
	}
	frame.selection = min(max(frame.selection, 0), len(options)-1)
	option := options[frame.selection]
	frame.draft = m.input.Value()
	if flow.choose(option) {
		m.input.SetValue("/" + flow.command.Name + " ")
		m.input.CursorEnd()
		m.status = option.Menu.Title
		return m.settleCommand(false, nil)
	}
	m.promptHistory = appendPromptHistory(m.promptHistory, flow.raw)
	return m.runCommandFlow(flow)
}

func (m model) backOrCancelCommandMenu() (model, tea.Cmd, bool) {
	if !m.commandMenuOpen() {
		return m, nil, true
	}
	if m.flow.back() {
		frame := m.flow.menu()
		m.input.SetValue(frame.draft)
		m.input.CursorEnd()
		m.status = frame.menu.Title
		return m.settleCommand(false, nil)
	}

	name := m.flow.command.Name
	m.flow = nil
	m.commandDismissed = true
	m.status = "/" + name + " selection cancelled"
	return m.settleCommand(false, m.input.Focus())
}

func (m *model) moveCommandMenuSelection(delta int) {
	if !m.commandMenuOpen() {
		return
	}
	frame := m.flow.menu()
	frame.selection = wrapIndex(frame.selection, delta, len(m.matchingCommandOptions()))
}

func currentSlashCommandOption(options []interaction.CommandOption) int {
	for index, option := range options {
		if option.Current {
			return index
		}
	}
	return 0
}

// showCredentialStep asks for the current credential field in the hidden composer.
func (m *model) showCredentialStep() {
	step := m.flow.credential()
	m.input.Reset()
	m.input.Placeholder = step.label + " (input hidden)"
	m.status = step.label + " required"
	if step.hint != "" {
		m.input.Placeholder = step.label + " (" + step.hint + ", input hidden)"
		m.status = step.title()
	}
}

func (m model) submitSecretInput() (model, tea.Cmd, bool) {
	if m.controllerClosed {
		return m.cancelSecretInput()
	}
	flow := m.flow
	problem, done := flow.submitCredential(m.input.Value())
	if problem != "" {
		m.status = problem
		return m, nil, true
	}
	if !done {
		m.showCredentialStep()
		return m.settleCommand(true, m.input.Focus())
	}
	m.resetCommandInput()
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Saving credential..."
	if flow.customLogin() {
		m.status = "Saving custom provider..."
	}
	return m.settleCommand(
		true,
		startSlashCommand(m.requests, m.controllerDone, flow.request),
	)
}

func (m model) cancelSecretInput() (model, tea.Cmd, bool) {
	prompt := m.flow.credential().label
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
	command interaction.Command,
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
	if !m.authActive() {
		m.flow = nil
	}
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""
}
