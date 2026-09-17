package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestViewCanvasKeepsHoverAndInputLocal(t *testing.T) {
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

func TestTerminalCodeClicksWriteOriginalClipboardPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := codeTestModel(t, 60)
	source := "\tclipboard  \r\n\n中文\n"
	m.entries = []transcriptEntry{{kind: entryAssistant, text: "```text\n" + source + "```", complete: true}}
	m.refreshViewport(true)
	mouse := codeButtonMouse(t, m, source)
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithWindowSize(m.width, m.height), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	waitForTerminalText(t, ctx, output, "[Copy]")
	for _, action := range []struct {
		mouse  tea.Mouse
		source string
	}{
		{mouse, source}, {codeLineMouse(t, m, 0, false), "\tclipboard  "},
	} {
		program.Send(tea.MouseMotionMsg(action.mouse))
		program.Send(tea.MouseClickMsg(action.mouse))
		program.Send(tea.MouseReleaseMsg(action.mouse))
		payload := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(action.source))
		copied := false
		for !copied {
			select {
			case frame := <-output:
				copied = strings.Contains(frame, payload)
			case <-ctx.Done():
				t.Fatal("terminal never emitted the original clipboard payload")
			}
		}
	}
}

func TestTerminalGuardRestoresHiddenUpdatesAndComposer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := newModel(nil, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.entries = []transcriptEntry{{kind: entryUser, text: "MAIN TRANSCRIPT"}}
	m.input.SetValue("draft")
	m.input.CursorEnd()
	m.refreshViewport(true)
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithWindowSize(m.width, m.height), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	waitForTerminalText(t, ctx, output, "MAIN TRANSCRIPT")
	reply := make(chan interaction.GuardReply, 1)
	program.Send(guardRequestMsg{req: &interaction.GuardRequest{
		Command: "review-command", Options: guardTestOptions(), Reply: reply,
	}})
	frame := waitForTerminalText(t, ctx, output, "Run this command?")
	if strings.Contains(frame, ansi.ShowCursor) {
		t.Fatal("permission renderer exposed the composer cursor")
	}
	if strings.Contains(ansi.Strip(frame), "MAIN TRANSCRIPT") || strings.Contains(ansi.Strip(frame), "draft") {
		t.Fatal("permission frame rendered the obscured conversation")
	}
	// Deliver a real run update while the guard owns the screen, then force a
	// resize repaint. Hidden content must wait until the reply restores it.
	program.Send(runBatchMsg{updates: []runUpdate{{output: "ARRIVED WHILE HIDDEN"}}})
	program.Send(tea.WindowSizeMsg{Width: 72, Height: 22})
	frame = waitForTerminalText(t, ctx, output, "Run this command?")
	if strings.Contains(ansi.Strip(frame), "ARRIVED WHILE HIDDEN") {
		t.Fatal("hidden update leaked through permission prompt")
	}
	program.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	waitForTerminalText(t, ctx, output, "Tell the agent")
	program.Send(tea.KeyPressMsg{Code: 'u', Text: "use public data"})
	waitForTerminalText(t, ctx, output, "use public data")
	program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForTerminalText(t, ctx, output, "ARRIVED WHILE HIDDEN")
	select {
	case got := <-reply:
		if got.OptionID != "deny" || got.Feedback != "use public data" {
			t.Fatalf("guard reply = %#v", got)
		}
	case <-ctx.Done():
		t.Fatal("guard did not receive its reply")
	}
	// Permission selection and feedback must never enter the composer, whose
	// focus must be restored even after the terminal was resized behind it.
	program.Send(tea.KeyPressMsg{Code: '!', Text: "!"})
	frame = waitForTerminalText(t, ctx, output, "draft!")
	if !strings.Contains(frame, ansi.ShowCursor) {
		t.Fatal("restored composer lost its terminal cursor")
	}
	if strings.Contains(ansi.Strip(frame), "use public data") {
		t.Fatal("permission feedback leaked into the restored composer")
	}
}

func TestTerminalSidePanelSwitchKeepsDraftAndMainUpdatesSeparate(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := sideTestModel(t, newFakeSideManager())
	m.entries = []transcriptEntry{{kind: entryUser, text: "MAIN CONVERSATION"}}
	m.promptHistory = []string{"MAIN HISTORY MUST STAY LOCAL"}
	m.refreshViewport(true)
	output := make(terminalFrameWriter, 256)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithWindowSize(m.width, m.height), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() { cancel(); <-done })
	waitForTerminalText(t, ctx, output, "MAIN CONVERSATION")
	program.Send(tea.KeyPressMsg{Code: '/', Text: "/btw"})
	program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	frame := waitForTerminalText(t, ctx, output, "BTW SIDE THREAD")
	if strings.Contains(ansi.Strip(frame), "MAIN CONVERSATION") {
		t.Fatal("side panel rendered the main transcript")
	}
	program.Send(tea.KeyPressMsg{Code: tea.KeyUp})
	program.Send(tea.KeyPressMsg{Code: 's', Text: "side draft"})
	waitForTerminalText(t, ctx, output, "side draft")
	program.Send(runBatchMsg{updates: []runUpdate{{output: "MAIN BACKGROUND UPDATE"}}})
	program.Send(tea.WindowSizeMsg{Width: 72, Height: 22})
	frame = waitForTerminalText(t, ctx, output, "BTW SIDE THREAD")
	for _, hidden := range []string{"MAIN BACKGROUND UPDATE", "MAIN HISTORY MUST STAY LOCAL"} {
		if strings.Contains(ansi.Strip(frame), hidden) {
			t.Fatalf("side panel exposed %q", hidden)
		}
	}
	program.Send(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt})
	frame = waitForTerminalText(t, ctx, output, "MAIN BACKGROUND UPDATE")
	if strings.Contains(ansi.Strip(frame), "side draft") {
		t.Fatal("side draft leaked to main composer")
	}
	// Reopening through the user command restores the side draft. If Up had
	// recalled main history, this composer would contain that history as well.
	program.Send(tea.KeyPressMsg{Code: '/', Text: "/btw"})
	program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	frame = waitForTerminalText(t, ctx, output, "side draft")
	for _, hidden := range []string{"MAIN BACKGROUND UPDATE", "MAIN HISTORY MUST STAY LOCAL"} {
		if strings.Contains(ansi.Strip(frame), hidden) {
			t.Fatalf("reopened side composer exposed %q", hidden)
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
