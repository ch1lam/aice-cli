package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
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
	switch msg.String() {
	case "y", "Y":
		m2 := m
		m2.resolveGuard(interaction.GuardDecisionAllowOnce)
		return m2, m2.nextGuardWait(), true
	case "a", "A":
		m2 := m
		m2.resolveGuard(interaction.GuardDecisionAllowAlways)
		return m2, m2.nextGuardWait(), true
	case "n", "N", "esc":
		m2 := m
		m2.resolveGuard(interaction.GuardDecisionDeny)
		return m2, m2.nextGuardWait(), true
	}
	switch msg.Code {
	case tea.KeyUp:
		if m.guardSelection > 0 {
			m.guardSelection--
		}
		return m, nil, true
	case tea.KeyDown:
		if m.guardSelection < 2 {
			m.guardSelection++
		}
		return m, nil, true
	case tea.KeyEnter:
		var decision interaction.GuardDecision
		switch m.guardSelection {
		case 0:
			decision = interaction.GuardDecisionAllowOnce
		case 1:
			decision = interaction.GuardDecisionAllowAlways
		default:
			decision = interaction.GuardDecisionDeny
		}
		m2 := m
		m2.resolveGuard(decision)
		return m2, m2.nextGuardWait(), true
	case tea.KeyEscape:
		m2 := m
		m2.resolveGuard(interaction.GuardDecisionDeny)
		return m2, m2.nextGuardWait(), true
	}
	return m, nil, true
}

func (m *model) resolveGuard(decision interaction.GuardDecision) {
	if m.guardPending == nil {
		return
	}
	req := m.guardPending
	m.guardPending = nil
	m.guardSelection = 0
	m.input.Focus()
	m.resizeLayout()
	// Non-blocking send; handler has buffer 1 and is waiting.
	select {
	case req.Reply <- decision:
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
	innerWidth := max(width-lipgloss.NewStyle().GetHorizontalFrameSize()-4, 20)
	detail := req.Reason
	if detail == "" {
		detail = "This action requires confirmation."
	}
	if req.Command != "" {
		detail += "\n" + mutedStyle.Render("$ "+req.Command)
	}
	if req.Path != "" && req.Command == "" {
		detail += "\n" + mutedStyle.Render(req.Path)
	}
	if req.RuleID != "" {
		detail += "\n" + mutedStyle.Render("rule: "+req.RuleID)
	}
	options := []string{"Allow once (y)", "Allow always (a)", "Deny (n)"}
	rows := make([]string, len(options))
	for i, opt := range options {
		prefix := "  "
		style := mutedStyle
		if i == m.guardSelection {
			prefix = "› "
			style = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
		}
		rows[i] = style.Render(prefix + opt)
	}
	body := strings.Join([]string{
		headerStyle.Render("⚠ Guard confirmation required"),
		bodyStyle.Render(detail),
		strings.Join(rows, "\n"),
		mutedStyle.Render("↑/↓ select · enter confirm · y/a/n quick · esc deny"),
	}, "\n\n")
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2).
		Width(min(innerWidth+4, 60))
	return style.Render(body)
}

func (m model) hasGuardPending() bool { return m.guardPending != nil }
