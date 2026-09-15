package tui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTerminalCanvasKeepsHoverAndInputLocal(t *testing.T) {
	for _, width := range []int{24, 40, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := foldTestModel()
			m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 36})
			m.viewport.GotoTop()
			before := m.View().Content
			mouse := paintedMouse(t, m, "✓ read")
			m = updateModel(t, m, tea.MouseMotionMsg(mouse))
			after := m.View().Content
			beforeRows, afterRows := strings.Split(before, "\n"), strings.Split(after, "\n")
			if len(beforeRows) != len(afterRows) || before == after {
				t.Fatal("hover must change the heading style without changing geometry")
			}
			for y := range beforeRows {
				if y != mouse.Y && beforeRows[y] != afterRows[y] {
					t.Fatalf("hover changed unrelated row %d", y)
				}
			}
			for _, state := range []string{"hover", "input", "blur", "expanded"} {
				switch state {
				case "input":
					m = updateModel(t, m, tea.KeyPressMsg{Code: '中', Text: "中文"})
				case "blur":
					m = updateModel(t, m, tea.BlurMsg{})
				case "expanded":
					m.setFoldExpanded(foldTarget{kind: foldTool, id: 2}, true)
					m.refreshViewport(false)
				}
				view := m.View()
				if view.BackgroundColor != nil || view.ForegroundColor != nil {
					t.Fatalf("%s changes the terminal palette", state)
				}
				if lipgloss.Width(view.Content) != width || lipgloss.Height(view.Content) != m.height {
					t.Fatalf("%s canvas does not cover the terminal", state)
				}
				assertCanvasBackground(t, view.Content, nil)
				if view.Cursor == nil || view.Cursor.Y >= m.height || view.Cursor.X >= width {
					t.Fatalf("%s lost the composer cursor used to anchor the IME", state)
				}
			}
		})
	}
}

// Decode SGR (including colon-delimited underlines) so resets cannot hide
// unpainted text or padding behind an otherwise correct-looking view string.
func assertCanvasBackground(t *testing.T, content string, want color.Color) {
	t.Helper()
	p := ansi.NewParser()
	var state byte
	var background color.Color
	for len(content) > 0 {
		seq, width, n, nextState := ansi.DecodeSequence(content, state, p)
		content, state = content[n:], nextState
		if strings.HasPrefix(seq, "\x1b[") && p.Command() == 'm' {
			params := p.Params()
			if len(params) == 0 {
				background = nil
			}
			for i := 0; i < len(params); i++ {
				switch code := params[i].Param(0); code {
				case 0, 49:
					background = nil
				case 38, 48, 58:
					var value color.Color
					consumed := ansi.ReadStyleColor(params[i:], &value)
					if consumed == 0 {
						t.Fatalf("invalid SGR color: %q", seq)
					}
					if code == 48 {
						background = value
					}
					i += consumed - 1
				default:
					for i < len(params)-1 && params[i].HasMore() {
						i++
					}
				}
			}
		}
		if width > 0 && background == nil {
			t.Fatalf("unpainted canvas cell %q", seq)
		}
		if width > 0 && want != nil {
			assertColor(t, background, want)
		}
	}
}

func TestTerminalDoesNotSetGlobalThemeColors(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := foldTestModel()
	mouse := paintedMouse(t, m, "✓ read")
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=1"}),
		tea.WithWindowSize(m.width, m.height), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	for _, step := range []struct {
		name string
		msg  tea.Msg
		text string
	}{
		{name: "initial", text: "README.md"},
		{name: "hover", msg: tea.MouseMotionMsg(mouse), text: "README.md"},
		{name: "committed input", msg: tea.KeyPressMsg{Code: '中', Text: "中文"}, text: "中文"},
	} {
		if step.msg != nil {
			program.Send(step.msg)
		}
		found := false
		for !found {
			select {
			case frame := <-output:
				for _, command := range []string{"\x1b]10;", "\x1b]11;", ansi.ResetForegroundColor, ansi.ResetBackgroundColor} {
					if strings.Contains(frame, command) {
						t.Fatalf("%s changes the terminal palette: %q", step.name, frame)
					}
				}
				found = strings.Contains(ansi.Strip(frame), step.text)
			case <-ctx.Done():
				t.Fatalf("terminal never rendered %s", step.name)
			}
		}
	}
}

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
