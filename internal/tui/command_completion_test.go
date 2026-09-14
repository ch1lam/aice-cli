package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func reasoningCompletionModel() model {
	return newModel(make(chan runRequest, 1), make(chan struct{}), SlashCommand{
		Name: "thinking", ArgumentHint: "<level>",
		Menu: &SlashCommandMenu{Title: "Select reasoning level", Options: []SlashCommandOption{
			{Label: "Extra High", Arguments: "xhigh", Current: true},
			{Label: "High", Arguments: "high"},
			{Label: "Medium", Arguments: "medium"},
			{Label: "Low", Arguments: "low"},
		}},
	})
}

func TestCommandCompletionChangesLevelsWhileTyping(t *testing.T) {
	t.Parallel()
	m := reasoningCompletionModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateModel(t, m, tea.PasteMsg{Content: "/tkg"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/thinking " || m.commandMenu == nil || !m.input.Focused() {
		t.Fatalf("command completion did not enter editable options: %q", m.input.Value())
	}
	if !strings.Contains(ansi.Strip(m.composerView(76)), "/thinking <level>") {
		t.Fatalf("missing inline hint: %q", m.composerView(76))
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "LW"})
	view := ansi.Strip(m.commandMenuView(76))
	if !strings.Contains(view, "Low") || strings.Contains(view, "Medium") || strings.Contains(view, "High") {
		t.Fatalf("fuzzy argument filter = %q", view)
	}
	if strings.Contains(ansi.Strip(m.composerView(76)), "<level>") {
		t.Fatal("placeholder remains over an entered argument")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/thinking low" || m.running {
		t.Fatalf("Tab should fill without execution: %q, running %v", m.input.Value(), m.running)
	}
	for range len("low ") {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	if m.commandMenu != nil || !m.slashCommandMenuVisible() || m.input.Value() != "/thinking" {
		t.Fatal("deleting the separator did not restore command suggestions")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.commandMenu == nil || len(m.matchingCommandOptions()) != 4 {
		t.Fatal("typing the separator did not restore all argument options")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.commandMenu != nil || m.input.Value() != "/thinking " {
		t.Fatal("Escape did not preserve the draft")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.commandMenu == nil || m.commandArgumentHint() != "<level>" {
		t.Fatal("Enter after Escape did not reopen the options and hint")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updateModel(t, m, tea.PasteMsg{Content: "hg"})
	if m.commandMenu == nil || len(m.matchingCommandOptions()) != 2 {
		t.Fatal("editing after dismissal did not reopen filtered options")
	}
}

func TestCommandCompletionDropsMenuForPastedPrompt(t *testing.T) {
	t.Parallel()
	m := reasoningCompletionModel()
	m = updateModel(t, m, tea.PasteMsg{Content: "/thinking "})
	m = updateModel(t, m, tea.PasteMsg{Content: strings.Repeat("pasted prompt\n", 100)})
	if m.commandMenu != nil || m.commandArgumentHint() != "" || len(m.pastes) == 0 {
		t.Fatal("pasted prompt retained an executable argument menu")
	}
}

func TestCommandCompletionExactValueWinsAndNoMatchDoesNotRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query, want string
	}{
		{name: "exact beats current fuzzy option", query: "high", want: "high"},
		{name: "fuzzy label", query: "etra", want: "xhigh"},
		{name: "fuzzy value", query: "mdm", want: "medium"},
		{name: "unknown", query: "no-such-level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan runRequest, 1)
			m := reasoningCompletionModel()
			m.requests = requests
			m = updateModel(t, m, tea.PasteMsg{Content: "/thinking " + tc.query})
			m, cmd, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if tc.want == "" {
				if cmd != nil || m.running || m.input.Value() != "/thinking "+tc.query ||
					!strings.Contains(ansi.Strip(m.commandMenuView(80)), "No matching options") {
					t.Fatal("unknown argument executed, lost the draft, or hid the empty result")
				}
				return
			}
			if cmd == nil || !m.running || !strings.Contains(m.status, "/thinking") {
				t.Fatal("matching argument did not execute")
			}

			cmd()
			if request := <-requests; request.command == nil || request.command.Arguments != tc.want {
				t.Fatalf("selected argument = %#v, want %q", request.command, tc.want)
			}
		})
	}
}

func TestCommandCompletionNestedFilterPreservesAction(t *testing.T) {
	t.Parallel()
	requests := make(chan runRequest, 1)
	m := newModel(requests, make(chan struct{}), SlashCommand{
		Name: "login", SecretPrompt: "API key",
		Menu: &SlashCommandMenu{Title: "Select provider", Options: []SlashCommandOption{{
			Label: "Example", Menu: &SlashCommandMenu{Title: "Select action", Options: []SlashCommandOption{
				{Label: "Replace key", Arguments: "example"},
				{Label: "Reuse saved key", Arguments: "example", UseSavedCredential: true},
			}},
		}}},
	})
	m = updateModel(t, m, tea.PasteMsg{Content: "/login xmp"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if len(m.commandMenu.frames) != 2 || !strings.Contains(m.commandArgumentHint(), "<action>") {
		t.Fatal("nested menu did not update its options and hint")
	}
	m = updateModel(t, m, tea.PasteMsg{Content: "rsk"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Value() != "/login xmp" || len(m.commandMenu.frames) != 1 {
		t.Fatal("back did not restore the parent query")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.PasteMsg{Content: "rsk"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/login example" || m.running || m.secretInput != nil {
		t.Fatal("Tab unexpectedly executed the nested action")
	}
	m, cmd, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.secretInput != nil {
		t.Fatal("Tab lost the reuse action when two actions share arguments")
	}
	cmd()
	if request := <-requests; request.command == nil || !request.command.UseSavedCredential {
		t.Fatalf("nested action metadata lost: %#v", request.command)
	}
}

func TestCommandHintPreservesDraftCursorAndLayout(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 40, 80} {
		m := reasoningCompletionModel()
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
		m = updateModel(t, m, tea.PasteMsg{Content: "/thinking "})
		before := *m.input.Cursor()
		plain := m.input.View()
		view := m.commandInputView(m.input.Width())
		if m.input.Value() != "/thinking " || *m.input.Cursor() != before || lipgloss.Height(view) != lipgloss.Height(plain) {
			t.Fatal("placeholder mutated draft, cursor, or editor height")
		}
		if lipgloss.Width(view) > m.input.Width() {
			t.Fatalf("hint overflow at terminal width %d: %q", width, view)
		}
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
		if strings.Contains(ansi.Strip(m.commandInputView(m.input.Width())), "<level>") {
			t.Fatal("hint remained while editing inside the command")
		}
	}
}
