package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const guardFeedbackPrompt = "Tell the agent what to do instead (optional):"

// guardPanel is the pending permission request and its dialog state. A
// zero panel has no request.
type guardPanel struct {
	request      *interaction.GuardRequest
	viewport     viewport.Model
	selection    int
	feedback     bool // Deny is collecting an optional note
	feedbackText string
}

func newGuardPanel(request *interaction.GuardRequest) guardPanel {
	panel := guardPanel{request: request, viewport: viewport.New()}
	panel.viewport.KeyMap = viewport.KeyMap{}
	panel.viewport.FillHeight = true
	return panel
}

type guardRequestMsg struct {
	req *interaction.GuardRequest
}

func waitForGuardRequest(requests <-chan interaction.GuardRequest) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-requests
		if !ok {
			return nil
		}
		// Copy to avoid referencing channel value after next receive.
		copied := req
		return guardRequestMsg{req: &copied}
	}
}

func (m model) handleGuardAction(match inputActionMatch) (model, tea.Cmd, bool) {
	if m.guard.request == nil {
		return m, nil, false
	}
	switch match.action {
	case inputActionGuardPage:
		if match.argument < 0 {
			m.guard.viewport.PageUp()
		} else {
			m.guard.viewport.PageDown()
		}
	case inputActionGuardBoundary:
		if match.argument < 0 {
			m.guard.viewport.GotoTop()
		} else {
			m.guard.viewport.GotoBottom()
		}
	case inputActionGuardSelect:
		m.guard.selection = clampIndex(m.guard.selection+match.argument, len(m.guard.request.Options))
	case inputActionGuardConfirm:
		return m.confirmGuardIndex(m.guard.selection)
	case inputActionGuardOption:
		return m.confirmGuardIndex(match.argument)
	case inputActionGuardSubmit:
		return m.submitGuardFeedback()
	case inputActionGuardBack:
		m.guard.feedback = false
		m.guard.feedbackText = ""
		m.resizeLayout()
	case inputActionGuardBackspace:
		runes := []rune(m.guard.feedbackText)
		if len(runes) > 0 {
			m.guard.feedbackText = string(runes[:len(runes)-1])
			m.resizeGuard()
		}
	}
	return m, nil, true
}

func (m model) handleGuardText(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.guard.feedback && message.Text != "" {
		m.guard.feedbackText += message.Text
		m.resizeGuard()
	}
	return m, nil, true
}

func (m model) confirmGuardIndex(index int) (model, tea.Cmd, bool) {
	if m.guard.request == nil || index < 0 || index >= len(m.guard.request.Options) {
		return m, nil, true
	}
	m.guard.selection = index
	if m.guard.request.Options[index].Deny {
		// Deny waits for an optional note instead of sending immediately.
		m.guard.feedback = true
		m.guard.feedbackText = ""
		m.resizeLayout()
		return m, nil, true
	}
	m.sendGuardReply(m.guard.request.Options[index].ID, "")
	return m, m.nextGuardWait(), true
}

func (m model) submitGuardFeedback() (model, tea.Cmd, bool) {
	if m.guard.request == nil || !m.guard.feedback {
		return m, nil, true
	}
	index := m.guard.selection
	if index < 0 || index >= len(m.guard.request.Options) {
		return m, nil, true
	}
	m.sendGuardReply(
		m.guard.request.Options[index].ID,
		strings.TrimSpace(m.guard.feedbackText),
	)
	return m, m.nextGuardWait(), true
}

func firstDenyGuardOption(options []interaction.GuardOption) int {
	for index, option := range options {
		if option.Deny {
			return index
		}
	}
	return -1
}

func (m *model) sendGuardReply(optionID, feedback string) {
	req := m.guard.request
	m.guard = guardPanel{viewport: m.guard.viewport}
	m.input.Focus()
	m.resizeLayout()
	m.refreshViewport(true)
	if req == nil {
		return
	}
	select {
	case req.Reply <- interaction.GuardReply{OptionID: optionID, Feedback: feedback}:
	default:
	}
}

func (m model) nextGuardWait() tea.Cmd {
	if m.guardRequests == nil {
		return nil
	}
	return waitForGuardRequest(m.guardRequests)
}

// guardLayout reserves controls before assigning any space to review content.
// The prompt owns the screen while pending, independent of transcript chrome.
func (m model) guardLayout(width int) (lipgloss.Style, int) {
	style := lipgloss.NewStyle()
	if width >= 60 && m.layoutHeight() >= 18 {
		style = style.Border(lipgloss.RoundedBorder()).BorderForeground(warningColor).Padding(0, 1)
	}
	return style.Width(width), max(width-style.GetHorizontalFrameSize(), 1)
}

func (m *model) resizeGuard() {
	if m.guard.request == nil {
		return
	}
	style, width := m.guardLayout(max(m.width-2*m.horizontalPadding(), 1))
	sections := []string{headerStyle.Render(guardTitle(m.guard.request))}
	if content := guardKeyLine(m.guard.request); content != "" {
		sections = append(sections, content)
	}
	if secondary := guardSecondaryLine(m.guard.request.Reason, m.guard.request.RuleID); secondary != "" {
		sections = append(sections, secondary)
	}
	for i, opt := range m.guard.request.Options {
		if guardOptionNeedsReview(opt, width) || opt.Detail != "" {
			sections = append(sections, fmt.Sprintf("Option %d: %s", i+1, opt.Label)+"\n"+opt.Detail)
		}
	}
	m.guard.viewport.SetWidth(width)
	// Hard-wrap once, preserving wide characters at line boundaries. The
	// viewport then scrolls complete display lines without cutting glyphs.
	content := ansi.Hardwrap(strings.Join(sections, "\n\n"), width, true)
	controls := m.guardControlsView(width)
	m.guard.viewport.SetHeight(max(1, m.layoutHeight()-style.GetVerticalFrameSize()-lipgloss.Height(controls)-1))
	m.guard.viewport.SetContent(content)
	m.guard.viewport.SetYOffset(m.guard.viewport.YOffset())
}

func (m model) guardView(width int) string {
	if m.guard.request == nil {
		return ""
	}
	style, innerWidth := m.guardLayout(width)
	hint := ""
	if !m.guard.viewport.AtTop() {
		hint += "↑ "
	}
	if !m.guard.viewport.AtBottom() {
		hint += "↓ "
	}
	return style.Render(strings.Join([]string{
		m.guard.viewport.View(), mutedStyle.Render(hint), m.guardControlsView(innerWidth),
	}, "\n"))
}

func (m model) guardControlsView(width int) string {
	footer := m.inputHelp(width, false)
	if m.guard.feedback {
		// Keep the editing tail visible without allowing a long note to move the
		// send/back controls off screen. The complete note remains in state.
		input := viewport.New(viewport.WithWidth(width), viewport.WithHeight(2))
		input.SetContent(ansi.Hardwrap(m.guard.feedbackText+guardCursorStyle.Render(" "), width, true))
		input.GotoBottom()
		return ansi.Hardwrap(mutedStyle.Render(guardFeedbackPrompt), width, true) + "\n" + input.View() + "\n" + mutedStyle.Render(footer)
	}
	return m.guardOptionsView(width) + "\n" + mutedStyle.Render(footer)
}

func guardOptionNeedsReview(opt interaction.GuardOption, width int) bool {
	return strings.ContainsAny(opt.Label, "\n\r\t") || lipgloss.Width(opt.Label)+5 > width
}

func guardTitle(req *interaction.GuardRequest) string {
	if req.Title != "" {
		return req.Title
	}
	if req.Command != "" {
		return "Run this command?"
	}
	if req.Path != "" {
		return "Allow access outside the workspace?"
	}
	if req.ToolName == "" {
		return "Allow this action?"
	}
	return fmt.Sprintf("Allow tool %q?", req.ToolName)
}

func guardKeyLine(req *interaction.GuardRequest) string {
	if req.Command != "" {
		return renderGuardCommand(req.Command, req.Highlight)
	}
	if req.Path != "" {
		return guardEmphasisStyle.Render(
			guardDisplayPath(req.Path),
		)
	}
	return ""
}

// guardDisplayPath shortens a path under the user home and uses "/" so the
// confirmation card shows "~/foo/bar" on every OS.
func guardDisplayPath(path string) string {
	return filepath.ToSlash(shellWorkingDirectory(path))
}

func renderGuardCommand(command, highlight string) string {
	prefix := "$ "
	display := command
	if highlight == "" {
		return guardEmphasisStyle.Render(prefix + display)
	}
	index := strings.Index(display, highlight)
	if index < 0 {
		return guardEmphasisStyle.Render(prefix + display)
	}
	end := index + len(highlight)
	return guardEmphasisStyle.Render(prefix) +
		guardEmphasisStyle.Render(display[:index]) +
		guardHighlightStyle.Render(display[index:end]) +
		guardEmphasisStyle.Render(display[end:])
}

func guardSecondaryLine(reason, ruleID string) string {
	reason = strings.TrimSpace(reason)
	ruleID = strings.TrimSpace(ruleID)
	switch {
	case reason != "" && ruleID != "":
		return mutedStyle.Render(reason) +
			mutedStyle.Render("  rule: "+ruleID)
	case reason != "":
		return mutedStyle.Render(reason)
	case ruleID != "":
		return mutedStyle.Render("rule: " + ruleID)
	default:
		return ""
	}
}

func (m model) guardOptionsView(width int) string {
	options := m.guard.request.Options
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		prefix := menuPrefix(i == m.guard.selection)
		style := mutedStyle
		if i == m.guard.selection {
			style = guardEmphasisStyle
		}
		label := opt.Label
		if guardOptionNeedsReview(opt, width) {
			label = fmt.Sprintf("Option %d (review above)", i+1)
			if width < 30 {
				label = fmt.Sprintf("Option %d ↑", i+1)
			}
		}
		row := style.Render(prefix + fmt.Sprintf("%d. %s", i+1, label))
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// A cancelled preflight must not leave an orphaned approval overlay behind.
type guardExpiredMsg struct{ reply chan interaction.GuardReply }

func waitForGuardExpiry(req *interaction.GuardRequest) tea.Cmd {
	if req.Done == nil {
		return nil
	}
	return func() tea.Msg { <-req.Done; return guardExpiredMsg{reply: req.Reply} }
}
