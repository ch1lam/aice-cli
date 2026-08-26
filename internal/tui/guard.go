package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const (
	guardFeedbackPrompt = "Tell the agent what to do instead (optional):"
	guardSelectFooter   = "↑/↓ select · 1-9/enter confirm · y first · n/esc deny"
	guardFeedbackFooter = "enter send · esc back"
)

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

func (m model) handleGuardKey(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.guardPending == nil {
		return m, nil, false
	}
	if m.guardFeedback {
		return m.handleGuardFeedbackKey(msg)
	}
	switch msg.Code {
	case tea.KeyUp:
		if m.guardSelection > 0 {
			m.guardSelection--
		}
		return m, nil, true
	case tea.KeyDown:
		if m.guardSelection+1 < len(m.guardPending.Options) {
			m.guardSelection++
		}
		return m, nil, true
	case tea.KeyEnter:
		return m.confirmGuardIndex(m.guardSelection)
	case tea.KeyEscape:
		return m.confirmGuardIndex(firstDenyGuardOption(m.guardPending.Options))
	}
	switch msg.String() {
	case "y", "Y":
		return m.confirmGuardIndex(0)
	case "n", "N":
		return m.confirmGuardIndex(firstDenyGuardOption(m.guardPending.Options))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.confirmGuardIndex(int(msg.String()[0] - '1'))
	}
	return m, nil, true
}

func (m model) handleGuardFeedbackKey(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	switch msg.Code {
	case tea.KeyEnter:
		return m.submitGuardFeedback()
	case tea.KeyEscape:
		m.guardFeedback = false
		m.guardFeedbackText = ""
		m.resizeLayout()
		return m, nil, true
	case tea.KeyBackspace:
		runes := []rune(m.guardFeedbackText)
		if len(runes) > 0 {
			m.guardFeedbackText = string(runes[:len(runes)-1])
		}
		return m, nil, true
	}
	if msg.Text != "" {
		m.guardFeedbackText += msg.Text
		return m, nil, true
	}
	return m, nil, true
}

func (m model) confirmGuardIndex(index int) (model, tea.Cmd, bool) {
	if m.guardPending == nil || index < 0 || index >= len(m.guardPending.Options) {
		return m, nil, true
	}
	m.guardSelection = index
	if m.guardPending.Options[index].Deny {
		// Deny waits for an optional note instead of sending immediately.
		m.guardFeedback = true
		m.guardFeedbackText = ""
		m.resizeLayout()
		return m, nil, true
	}
	m.sendGuardReply(m.guardPending.Options[index].ID, "")
	return m, m.nextGuardWait(), true
}

func (m model) submitGuardFeedback() (model, tea.Cmd, bool) {
	if m.guardPending == nil || !m.guardFeedback {
		return m, nil, true
	}
	index := m.guardSelection
	if index < 0 || index >= len(m.guardPending.Options) {
		return m, nil, true
	}
	m.sendGuardReply(
		m.guardPending.Options[index].ID,
		strings.TrimSpace(m.guardFeedbackText),
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
	req := m.guardPending
	m.guardPending = nil
	m.guardSelection = 0
	m.guardFeedback = false
	m.guardFeedbackText = ""
	m.input.Focus()
	m.resizeLayout()
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

func (m model) guardView(width int) string {
	if m.guardPending == nil {
		return ""
	}
	req := m.guardPending
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2)
	innerWidth := max(width-style.GetHorizontalFrameSize(), 20)

	sections := make([]string, 0, 5)
	sections = append(sections, headerStyle.Render(guardTitle(req)))
	if key := guardKeyLine(req, innerWidth); key != "" {
		sections = append(sections, key)
	}
	if secondary := guardSecondaryLine(req.Reason, req.RuleID); secondary != "" {
		sections = append(sections, secondary)
	}
	sections = append(sections, m.guardOptionsView())
	if m.guardFeedback {
		sections = append(sections, m.guardFeedbackView(innerWidth))
		sections = append(sections, mutedStyle.Render(guardFeedbackFooter))
	} else {
		sections = append(sections, mutedStyle.Render(guardSelectFooter))
	}
	return style.Width(width).Render(strings.Join(sections, "\n\n"))
}

func guardTitle(req *interaction.GuardRequest) string {
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

func guardKeyLine(req *interaction.GuardRequest, width int) string {
	if req.Command != "" {
		return renderGuardCommand(req.Command, req.Highlight, width)
	}
	if req.Path != "" {
		return guardEmphasisStyle.Render(
			truncateTerminalText(guardDisplayPath(req.Path), width),
		)
	}
	return ""
}

// guardDisplayPath shortens a path under the user home and uses "/" so the
// confirmation card shows "~/foo/bar" on every OS.
func guardDisplayPath(path string) string {
	return filepath.ToSlash(shellWorkingDirectory(path))
}

func renderGuardCommand(command, highlight string, width int) string {
	prefix := "$ "
	display := truncateTerminalText(command, max(width-lipgloss.Width(prefix), 1))
	if highlight == "" || !strings.Contains(command, highlight) {
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

func (m model) guardOptionsView() string {
	options := m.guardPending.Options
	rows := make([]string, 0, len(options))
	for i, opt := range options {
		prefix := "  "
		style := mutedStyle
		if i == m.guardSelection {
			prefix = "› "
			style = guardEmphasisStyle
		}
		row := style.Render(prefix + fmt.Sprintf("%d. %s", i+1, opt.Label))
		if opt.Detail != "" {
			row += "\n" + mutedStyle.Render("    "+opt.Detail)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func (m model) guardFeedbackView(width int) string {
	input := m.guardFeedbackText + guardCursorStyle.Render(" ")
	return mutedStyle.Render(guardFeedbackPrompt) + "\n" +
		bodyStyle.Width(max(width, 1)).Render(input)
}
