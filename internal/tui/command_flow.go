package tui

import (
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// commandFlow is one interactive command's local state, shared by the
// composer and the Settings modal. It moves from the option-menu stack to the
// hidden credential form, then to the running command's transient prompts.
// Hosts own text entry, rendering and starting the command; no value here
// enters the transcript, Session history or prompt history.
type commandFlow struct {
	raw     string // composer text that opened the flow; empty in Settings
	command SlashCommand
	request SlashCommandRequest
	menus   []commandMenuFrame
	form    []credentialStep // non-nil while collecting credentials
	step    int
	// input carries manual replies to the running command; prompt is its
	// latest request and selection the highlighted prompt menu option.
	input     chan string
	prompt    *interaction.AuthPrompt
	selection int
}

type commandMenuFrame struct {
	menu      SlashCommandMenu
	selection int
	draft     string // composer text restored when returning to this level
}

// Composer command phases are projections of the single flow.

func (m model) commandMenuOpen() bool { return m.flow != nil && m.flow.menu() != nil }

func (m model) secretEntry() bool { return m.flow != nil && m.flow.form != nil }

func (m model) authActive() bool { return m.flow != nil && m.flow.input != nil }

// authPrompt is the running composer command's current transient prompt.
func (m model) authPrompt() *interaction.AuthPrompt {
	if !m.authActive() {
		return nil
	}
	return m.flow.prompt
}

func newCommandFlow(raw string, command SlashCommand, request SlashCommandRequest) *commandFlow {
	f := &commandFlow{raw: raw, command: command, request: request}
	if command.Menu != nil {
		f.pushMenu(*command.Menu)
	}
	return f
}

func (f *commandFlow) pushMenu(menu SlashCommandMenu) {
	f.menus = append(f.menus, commandMenuFrame{menu: menu, selection: currentSlashCommandOption(menu.Options)})
}

// menu is the innermost option level, or nil once options are chosen.
func (f *commandFlow) menu() *commandMenuFrame {
	if len(f.menus) == 0 {
		return nil
	}
	return &f.menus[len(f.menus)-1]
}

// back returns to the previous option level; false at the first level.
func (f *commandFlow) back() bool {
	if len(f.menus) <= 1 {
		return false
	}
	f.menus = f.menus[:len(f.menus)-1]
	return true
}

// choose opens an option's nested level and reports true, or applies a leaf
// option to the request and closes the menus.
func (f *commandFlow) choose(option SlashCommandOption) bool {
	if option.Menu != nil && len(option.Menu.Options) > 0 {
		f.pushMenu(*option.Menu)
		return true
	}
	f.request.Arguments = option.Arguments
	f.request.UseSavedCredential = option.UseSavedCredential
	f.request.LoginMethod = option.LoginMethod
	f.menus = nil
	return false
}

// needsCredential reports whether the chosen request must collect a secret
// before it runs. Saved credentials and account login skip the form.
func (f *commandFlow) needsCredential() bool {
	return f.command.SecretPrompt != "" && !f.request.UseSavedCredential && f.request.LoginMethod == ""
}

// activeMenu is the menu currently answering selection keys: the running
// command's prompt menu, or the innermost option level.
func (f *commandFlow) activeMenu() (*SlashCommandMenu, *int) {
	if f.prompt != nil {
		return f.prompt.Menu, &f.selection
	}
	if frame := f.menu(); frame != nil {
		return &frame.menu, &frame.selection
	}
	return nil, nil
}

// showPrompt replaces the running command's prompt; its menu starts at the top.
func (f *commandFlow) showPrompt(prompt interaction.AuthPrompt) {
	f.prompt = &prompt
	f.selection = 0
}

// reply hands one manual answer to the running command without blocking.
// It fails while the previous answer is still unread.
func (f *commandFlow) reply(value string) bool {
	select {
	case f.input <- value:
		return true
	default:
		return false
	}
}

// credentialStep is one field of the hidden credential form.
type credentialStep struct {
	label, hint string
	secret      bool // hosts that can show plain input still mask this field
	optional    bool
	check       func(string) string
	assign      func(*SlashCommandRequest, string)
}

func (s credentialStep) title() string {
	if s.hint == "" {
		return s.label
	}
	return s.label + " (" + s.hint + ")"
}

// Custom login collects endpoint, API key and model as separate fields. Empty
// values keep the current configuration; the application validates again.
var customLoginSteps = []credentialStep{
	{label: "Custom endpoint URL", hint: "e.g. http://localhost:11434/v1, blank keeps current", optional: true,
		check: func(value string) string {
			if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
				return "Endpoint must start with http:// or https:// (or leave blank)"
			}
			return ""
		},
		assign: func(r *SlashCommandRequest, value string) { r.CustomEndpoint = value }},
	{label: "API key", hint: "leave empty for Ollama", secret: true, optional: true,
		assign: func(r *SlashCommandRequest, value string) { r.Secret = value }},
	{label: "Model name", hint: "e.g. llama3.1:8b, blank keeps current", optional: true,
		check: func(value string) string {
			if strings.ContainsAny(value, " \t") {
				return "Model name must not contain whitespace"
			}
			return ""
		},
		assign: func(r *SlashCommandRequest, value string) { r.CustomModel = value }},
}

// beginCredential starts the hidden form for the chosen request.
func (f *commandFlow) beginCredential() {
	f.menus = nil
	f.step = 0
	f.form = []credentialStep{{label: f.command.SecretPrompt, secret: true,
		assign: func(r *SlashCommandRequest, value string) { r.Secret = value }}}
	if f.customLogin() {
		f.form = customLoginSteps
	}
}

func (f *commandFlow) customLogin() bool {
	return f.request.Name == "login" && f.request.Arguments == "custom"
}

// credential is the form field awaiting input.
func (f *commandFlow) credential() credentialStep { return f.form[f.step] }

// formStep identifies the form field for input lifetimes; -1 outside the form.
func (f *commandFlow) formStep() int {
	if f.form == nil {
		return -1
	}
	return f.step
}

// submitCredential records the current field. It returns a problem to show
// instead, or done once the request carries every field.
func (f *commandFlow) submitCredential(value string) (problem string, done bool) {
	step := f.credential()
	value = strings.TrimSpace(value)
	switch {
	case value == "" && !step.optional:
		return step.label + " must not be blank", false
	case strings.ContainsAny(value, "\r\n"):
		return step.label + " must be entered on one line", false
	}
	if step.check != nil {
		if problem := step.check(value); problem != "" {
			return problem, false
		}
	}
	step.assign(&f.request, value)
	if f.step++; f.step < len(f.form) {
		return "", false
	}
	f.form = nil
	return "", true
}
