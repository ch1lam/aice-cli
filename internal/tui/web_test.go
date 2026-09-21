package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestWebCommandPromptsMaskSecretsAndStayOutOfHistory(t *testing.T) {
	requests := make(chan runRequest, 1)
	m := newModel(requests, make(chan struct{}))
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	command := SlashCommand{Name: "web", Interactive: true, Menu: &SlashCommandMenu{Title: "Web", Options: []SlashCommandOption{{Label: "Add Exa instance…", Arguments: "add"}}}}
	m.commands = slashCommandCatalog([]SlashCommand{command})
	// A menu choice on an interactive command starts the command with its
	// arguments instead of reopening the option menu.
	m, cmd, handled := m.submitSlashCommand("/web add", SlashCommandRequest{Name: "web", Arguments: "add"})
	if !handled || cmd == nil || m.commandMenu != nil {
		t.Fatal("command not started")
	}
	cmd()
	request := <-requests
	if request.command == nil || request.command.Auth == nil || request.command.Arguments != "add" {
		t.Fatalf("request = %+v", request.command)
	}
	menu := interaction.AuthPrompt{Title: "Add Exa instance exa-main", Menu: &interaction.CommandMenu{Options: []interaction.CommandOption{{Label: "Enter an API key now", Arguments: "key"}, {Label: "Environment variable", Arguments: "env"}}}}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{auth: &menu}}})
	if !strings.Contains(m.transcriptView(), "Enter an API key now") {
		t.Fatal("menu missing")
	}
	m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if value := <-request.command.Auth.Input; value != "key" {
		t.Fatal(value)
	}
	secret := interaction.AuthPrompt{Title: "Exa API key for exa-main", AllowInput: true, InputLabel: "Exa API key (input hidden)"}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{auth: &secret}}})
	m = updateModel(t, m, tea.PasteMsg{Content: "sk-very-secret"})
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "sk-very-secret") || !strings.Contains(view, "••••") {
		t.Fatalf("secret visible in composer: %q", view)
	}
	m, _, _ = m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if value := <-request.command.Auth.Input; value != "sk-very-secret" {
		t.Fatal(value)
	}
	if m.authPrompt != nil || !strings.Contains(m.status, "/web") {
		t.Fatalf("single-shot prompt retained: %+v %q", m.authPrompt, m.status)
	}
	if len(m.promptHistory) != 0 {
		t.Fatal("web input entered prompt history")
	}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true, output: "Added Exa instance exa-main"}}})
	if m.running || m.authInput != nil {
		t.Fatal("web UI retained command state")
	}
	if view := m.transcriptView(); strings.Contains(view, "sk-very-secret") || !strings.Contains(view, "Added Exa instance exa-main") {
		t.Fatalf("transcript = %q", view)
	}
}

func TestToolEvidenceViewEscapesAndClips(t *testing.T) {
	t.Parallel()
	display := interaction.EvidenceDisplay{
		Sources: []interaction.SourceDisplay{
			{Title: "Docs \x1b[31mRED\x1b[0m \x1b]8;;http://evil\x07link\x1b]8;;\x07", URL: "https://example.com/" + strings.Repeat("path/", 40), Kinds: []string{"excerpt"}},
			{Title: "中文标题 测试", URL: "https://例え.jp/x", Kinds: []string{"document"}},
		},
		Warnings: []string{"result 2 dropped: \x1b[2Junsupported scheme"},
	}
	view := toolEvidenceView(display, 60)
	if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "\x1b]8") || strings.Contains(view, "\x1b[2J") {
		t.Fatalf("control sequences leaked: %q", view)
	}
	if !strings.Contains(view, "2 sources") || !strings.Contains(view, "中文标题 测试") || !strings.Contains(view, "· document") || !strings.Contains(view, "warning: result 2 dropped") {
		t.Fatalf("view = %q", view)
	}
	for _, row := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(row); w > 60 {
			t.Fatalf("row width %d exceeds panel: %q", w, row)
		}
	}
	if toolEvidenceView(interaction.EvidenceDisplay{}, 60) != "" {
		t.Fatal("empty evidence rendered")
	}
	entry := transcriptEntry{kind: entryTool, toolName: "web_search", toolDetail: "query", toolDone: true, toolOutput: interaction.ToolOutputDisplay{Text: "results", Available: true}, toolEvidence: &display}
	m := newModel(nil, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if body := m.toolBodyView(entry); !strings.Contains(body, "2 sources") {
		t.Fatalf("tool body lacks sources: %q", body)
	}
}
