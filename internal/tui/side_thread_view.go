package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Display-only mirrors of the app-side lifecycle windows. The registry in
// internal/app remains the enforcement authority; these constants only shape
// the status text.
const sideDisplayExpiryMinutes = 120

func (m model) sideThreadView() string {
	parts := []transcriptViewPart{{content: m.sideThreadIntro()}}
	if thread := m.side.activeThread(); thread != nil {
		for index, entry := range thread.entries {
			parts = append(parts, transcriptViewPart{
				content: m.sideQuestionView(entry.question),
			})
			if answer := m.sideAnswerView(
				entry,
				thread.isRunning && index == thread.assistantEntry,
			); answer != "" {
				parts = append(parts, transcriptViewPart{content: answer})
			}
		}
	}
	return joinTranscriptViewParts(parts)
}

func (m model) sideThreadIntro() string {
	title := headerStyle.Render("↗ BTW SIDE THREAD")
	detail := mutedStyle.Render(
		"Ephemeral · no tools · excluded from the main Session",
	)
	if thread := m.side.activeThread(); thread != nil {
		detail += "\n" + infoStyle.Render(
			fmt.Sprintf("#%d %s", thread.id, sanitizeSideTitle(thread.title)),
		)
	} else if len(m.side.threads) == 0 {
		detail += "\n\n" + bodyStyle.Render(
			"Ask about context AICE already gathered while the main task keeps running.",
		)
	}
	if strings.TrimSpace(m.side.notice) != "" {
		detail += "\n" + infoStyle.Render(m.side.notice)
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(title + "\n" + detail)
}

func (m model) sideQuestionView(question string) string {
	bodyWidth := max(m.contentWidth()-userStyle.GetHorizontalFrameSize(), 1)
	body := userStyle.Width(bodyWidth).Render(question)
	return lipgloss.NewStyle().Padding(0, 1).Render(
		labelStyle.Render("YOU / BTW") + "\n" + body,
	)
}

func (m model) sideAnswerView(entry sideThreadEntry, active bool) string {
	bodyWidth := max(
		m.contentWidth()-assistantBodyStyle.GetHorizontalFrameSize(),
		1,
	)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(entry.thinking) != "" {
		thinkingWidth := max(
			bodyWidth-thinkingStyle.GetHorizontalFrameSize(),
			1,
		)
		parts = append(parts, assistantBodyStyle.Render(
			thinkingStyle.Width(thinkingWidth).Render(entry.thinking),
		))
	}
	if strings.TrimSpace(entry.answer) != "" {
		parts = append(parts, assistantBodyStyle.Render(
			renderMarkdown(entry.answer, m.contentWidth()),
		))
	}
	if entry.err != "" {
		parts = append(parts, errorStyle.Render("✕ "+entry.err))
	}
	if entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		strings.TrimSpace(entry.thinking) == "" &&
		entry.err == "" {
		parts = append(parts, mutedStyle.Render("No text response"))
	}
	if active &&
		!entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		entry.err == "" {
		parts = append(parts, assistantBodyStyle.Render(
			m.spinner.View()+" "+mutedStyle.Render(m.side.notice),
		))
	}
	if len(parts) == 0 {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(
		headerStyle.Render("✦ AICE / BTW") + "\n\n" +
			strings.Join(parts, "\n"),
	)
}

// sanitizeSideTitle strips control characters and ANSI escapes from a thread
// title before rendering.
func sanitizeSideTitle(title string) string {
	title = ansi.Strip(title)
	title = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, title)
	return strings.TrimSpace(title)
}

func (m model) sideStatusLine(width int) string {
	left := ""
	switch {
	case m.side.confirm != nil:
		left = mutedStyle.Render("y/enter end thread · n/esc keep")
	case m.side.menu != nil:
		left = mutedStyle.Render("↑/↓ select · enter open · esc cancel")
	case m.side.activeID == 0:
		if m.side.newPending != nil {
			left = mutedStyle.Render("esc close")
		} else {
			left = mutedStyle.Render("enter ask · esc close")
		}
	default:
		thread := m.side.activeThread()
		switch {
		case thread == nil:
			left = mutedStyle.Render("esc close")
		case thread.isRunning:
			left = mutedStyle.Render("esc close · ctrl+C cancel · ctrl+D end thread")
		case thread.readOnly():
			left = mutedStyle.Render("read-only · esc close · ctrl+D end thread")
		default:
			left = mutedStyle.Render(
				"enter ask · shift+enter newline · ctrl+D end thread · esc close",
			)
		}
	}
	if m.help.ShowAll {
		left = mutedStyle.Render(
			"enter ask  shift+enter newline  pgup/pgdn scroll  esc close  ctrl+C cancel  ctrl+D end thread",
		)
	}
	if line, ok := alignStatusLine(left, m.modelStatus(), width); ok {
		return line
	}
	if line, ok := alignStatusLine("", m.modelStatus(), width); ok {
		return line
	}
	return ""
}

func (m model) sideMenuView(width int) string {
	menu := m.side.menu
	if menu == nil || len(menu.options) == 0 {
		return ""
	}
	rows := make([]slashMenuRow, 0, len(menu.options)+1)
	rows = append(rows, slashMenuRow{
		label: "＋ " + sideNewTitle,
	})
	for _, thread := range menu.options {
		label := fmt.Sprintf(
			"#%d %s",
			thread.ID,
			sanitizeSideTitle(thread.Title),
		)
		if state := m.side.thread(thread.ID); state != nil && state.hasUnread {
			label = "● " + label
		}
		rows = append(rows, slashMenuRow{
			label:       label,
			description: sideThreadStatusText(thread, time.Now()),
		})
	}
	return renderSlashMenuRows(
		width,
		"BTW THREADS",
		"↑/↓ select · enter open · esc cancel",
		rows,
		min(max(menu.selection, 0), len(rows)-1),
	)
}

// sideThreadStatusText renders the menu status for one registry snapshot.
// The registry clock is authoritative; wall time only shapes the display.
func sideThreadStatusText(thread interaction.SideThread, now time.Time) string {
	elapsed := now.Sub(thread.LastActiveAt)
	if elapsed < 0 {
		elapsed = 0
	}
	switch thread.Status {
	case interaction.SideThreadRunning:
		return "Answering…"
	case interaction.SideThreadReadOnly:
		remaining := sideDisplayExpiryMinutes*time.Minute - elapsed
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Sprintf(
			"Read-only · expires in %dm",
			int(remaining.Minutes()),
		)
	default:
		return fmt.Sprintf("Follow-up · %dm idle", int(elapsed.Minutes()))
	}
}

func sideReadOnlyNotice(thread *sideThreadState) string {
	elapsed := time.Since(thread.lastActiveAt)
	if elapsed < 0 {
		elapsed = 0
	}
	remaining := sideDisplayExpiryMinutes*time.Minute - elapsed
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf(
		"Read-only · expires in %dm · /btw starts a new thread",
		int(remaining.Minutes()),
	)
}

func (m model) sideConfirmView(width int) string {
	if m.side.confirm == nil {
		return ""
	}
	rows := []slashMenuRow{{
		label:       "End this BTW thread",
		description: "Its running answer will be cancelled",
	}}
	return renderSlashMenuRows(
		width,
		"END BTW THREAD?",
		"y/enter confirm · n/esc keep",
		rows,
		0,
	)
}
