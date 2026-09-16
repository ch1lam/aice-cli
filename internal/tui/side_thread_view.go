package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Display-only mirrors of the app-side lifecycle windows. The registry in
// internal/app remains the enforcement authority; these constants only shape
// the status text.
const sideDisplayExpiryMinutes = 120

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
	return m.transcriptContentView(title + "\n" + detail)
}

func (m model) sideQuestionView(question string) string {
	return m.userMessageView(question)
}

func (m model) sideAnswerContent(entry sideThreadEntry, active bool) transcriptContent {
	var content transcriptContent
	if thinking := entry.presentation.thinkingView(entry.thinking, m.contentWidth(), !entry.complete); thinking != "" {
		content.appendText(thinking)
	}
	if answer := entry.presentation.textContent(entry.answer, m.contentWidth()); answer.view != "" {
		content.append(answer, "\n")
	}
	if entry.err != "" {
		content.appendText(assistantBodyStyle.Render(errorStyle.Render("✕ " + entry.err)))
	}
	if entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		strings.TrimSpace(entry.thinking) == "" &&
		entry.err == "" {
		content.appendText(assistantBodyStyle.Render(mutedStyle.Render("No text response")))
	}
	if active &&
		!entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		entry.err == "" {
		content.appendText(assistantBodyStyle.Render(
			m.spinner.View() + " " + mutedStyle.Render(m.side.notice),
		))
	}
	if content.view == "" {
		return content
	}
	heading := transcriptContent{view: assistantBodyStyle.Render(headerStyle.Render("✦ AICE / BTW"))}
	heading.append(content, "\n\n")
	return heading.pad(1, 1)
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
		left = mutedStyle.Render("y/Enter end thread · n/Esc keep")
	case m.side.menu != nil:
		left = mutedStyle.Render("↑/↓ select · Enter open · Esc cancel")
	case m.side.activeID == 0:
		if m.side.newPending != nil {
			left = mutedStyle.Render("Esc cancel · Alt+Esc close")
		} else {
			left = mutedStyle.Render("Enter ask · Esc close")
		}
	default:
		thread := m.side.activeThread()
		switch {
		case thread == nil:
			left = mutedStyle.Render("Esc close")
		case thread.isRunning:
			left = mutedStyle.Render("Esc cancel · Alt+Esc close · Ctrl+d end thread")
		case thread.readOnly():
			left = mutedStyle.Render("read-only · Esc close · Ctrl+d end thread")
		default:
			left = mutedStyle.Render(
				"Enter ask · Shift+Enter newline · Ctrl+d end thread · Esc close",
			)
		}
	}
	if m.side.menu == nil && m.side.confirm == nil {
		left += mutedStyle.Render(" · Ctrl+c clear/quit")
	}
	if m.help.ShowAll {
		left = mutedStyle.Render(
			"Enter ask  Shift+Enter newline  PgUp/PgDn scroll  Esc cancel/close  Alt+Esc close  Ctrl+c clear/quit  Ctrl+d end thread",
		)
	}
	return ansi.Truncate(left, width, "…")
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
		"↑/↓ select · Enter open · Esc cancel",
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
		"y/Enter confirm · n/Esc keep",
		rows,
		0,
	)
}
