package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the terminal writer, not just View(): a correct view can still lose
// glyphs when a cell diff erases one half of a previously drawn wide character.
func TestTerminalRepaintsChangedWideText(t *testing.T) {
	for _, text := range []string{"甲乙丙丁", "中文 English 中文"} {
		t.Run(text, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output := make(terminalFrameWriter, 128)
			program := tea.NewProgram(terminalTextModel(text),
				tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
				tea.WithEnvironment([]string{"TERM=xterm-256color"}),
				tea.WithWindowSize(80, 12), tea.WithoutSignalHandler(),
			)
			done := make(chan error, 1)
			go func() {
				_, err := program.Run()
				done <- err
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			waitForTerminalText(t, ctx, output, text)
			program.Send(terminalTextModel(text + "新增"))
			frame := waitForTerminalText(t, ctx, output, "新增")
			// Wide lines must be emitted from a known line boundary, including
			// their unchanged prefix, instead of resuming inside old glyphs.
			if !strings.Contains(ansi.Strip(frame), text+"新增") {
				t.Fatalf("wide line was only partially repainted: %q", frame)
			}
		})
	}
}

type terminalTextModel string

func (terminalTextModel) Init() tea.Cmd { return nil }

func (m terminalTextModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, ok := msg.(terminalTextModel); ok {
		m = next
	}
	return m, nil
}

func (m terminalTextModel) View() tea.View {
	view := tea.NewView(lipgloss.NewStyle().Foreground(primaryTextColor).
		Background(inkBlackColor).Render(string(m)))
	view.AltScreen = true
	return view
}

type terminalFrameWriter chan string

func (w terminalFrameWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func waitForTerminalText(t *testing.T, ctx context.Context, output terminalFrameWriter, text string) string {
	t.Helper()
	for {
		select {
		case frame := <-output:
			if strings.Contains(ansi.Strip(frame), text) {
				return frame
			}
		case <-ctx.Done():
			t.Fatalf("terminal never rendered %q", text)
			return ""
		}
	}
}
