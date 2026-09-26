package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestDesktopActivityLifecycleAndFolds(t *testing.T) {
	t.Parallel()
	m := newModel(nil, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.running = true
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{
		ID: "d", Name: "desktop_act", Detail: `{"text":"PRIVATE INPUT"}`,
		Desktop: &interaction.DesktopDisplay{App: "Notes", Phase: "Background requested"},
	}})
	if got := ansi.Strip(m.transcriptView()); !strings.Contains(got, "Computer Use · Notes · Background requested") || strings.Contains(got, "PRIVATE INPUT") {
		t.Fatal(got)
	}
	m.expandAllDetails(true)
	if !strings.Contains(ansi.Strip(m.transcriptView()), "PRIVATE INPUT") {
		t.Fatal("expanded parameters absent")
	}
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{
		ID: "d", Desktop: &interaction.DesktopDisplay{App: "Notes", Phase: "Returned"},
	}})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: "Considering result"}})
	if got := ansi.Strip(m.composerView(96)); !strings.Contains(got, "Computer Use · Notes · Planning") {
		t.Fatal(got)
	}
	m.status = "Retrying in 1s (1/3)..."
	if got := m.desktopActivityView(96); !strings.Contains(ansi.Strip(got), "Waiting for model") {
		t.Fatal(got)
	}
	m.cancelRequested = true
	if got := m.desktopActivityView(96); !strings.Contains(ansi.Strip(got), "Stopping") {
		t.Fatal(got)
	}
	m.finishRun(context.Canceled)
	if m.desktopActivity != nil || m.desktopActivityView(96) != "" {
		t.Fatal("terminal run retained desktop activity")
	}
	m.running = true
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	if m.desktopActivityView(96) != "" {
		t.Fatal("new run inherited desktop activity")
	}
}

func TestDesktopActivityRetainsFailuresAndHidesUnrelatedWork(t *testing.T) {
	t.Parallel()
	m := newModel(nil, nil)
	m.running = true
	for _, phase := range []string{"Outcome unknown", "Needs setup", "Observation failed"} {
		m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{Failed: true, Desktop: &interaction.DesktopDisplay{Phase: phase}}})
		m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
		if !strings.Contains(ansi.Strip(m.desktopActivityView(80)), phase) {
			t.Fatal("failure changed to planning")
		}
	}
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "read", Name: "read"}})
	if m.desktopActivity != nil {
		t.Fatal("ordinary tool kept desktop activity")
	}
	m.setDesktopActivity(&interaction.DesktopDisplay{App: "Notes", Phase: "Planning"}, false, false)
	m.side.isVisible = true
	if strings.Contains(ansi.Strip(m.composerView(80)), "Computer Use") {
		t.Fatal("main status leaked into BTW composer")
	}
	m.side.isVisible = false
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "unfinished", Name: "desktop_act", Desktop: &interaction.DesktopDisplay{Phase: "Background requested"}}})
	m.finishRun(context.Canceled)
	entry := m.entries[len(m.entries)-2] // last entry is cancellation notice
	if !entry.toolDone || !entry.toolError || entry.toolDesktop.Phase != "Result unavailable" {
		t.Fatal("pending request looked completed", entry)
	}
}

func TestDesktopActivityNarrowAndUntrustedLabels(t *testing.T) {
	t.Parallel()
	display := interaction.DesktopDisplay{App: "中文\n\x1b]52;c;PAYLOAD\a" + strings.Repeat("界", 200), Phase: "Outcome unknown"}
	for _, width := range []int{20, 28, 76, 116} {
		label := desktopActivityLabel(display, width)
		if ansi.StringWidth(label) > width || strings.ContainsAny(label, "\n\r\x1b\a") {
			t.Fatalf("unsafe/wide %q", label)
		}
		if width >= 28 && !strings.Contains(label, "Outcome unknown") {
			t.Fatal("app displaced outcome", label)
		}
	}
}

func TestTerminalDesktopActivityAcrossModelWaitAndStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	m := newModel(nil, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.running = true
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithWindowSize(100, 30), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	send := func(event DisplayEvent, text string) {
		program.Send(runBatchMsg{updates: []runUpdate{{event: event}}})
		waitForTerminalText(t, ctx, output, text)
	}
	send(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "d", Name: "desktop_act", Desktop: &interaction.DesktopDisplay{App: "Notes", Phase: "Waiting"}}}, "Computer Use · Notes · Waiting")
	send(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{ID: "d", Desktop: &interaction.DesktopDisplay{App: "Notes", Phase: "Returned"}}}, "Planning")
	program.Send(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{Kind: DisplayEventAssistantStart}}}})
	send(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: "MODEL DECIDING"}}, "MODEL DECIDING")
	program.Send(runBatchMsg{updates: []runUpdate{{done: true, err: context.Canceled}}})
	waitForTerminalText(t, ctx, output, "Response cancelled")
}
