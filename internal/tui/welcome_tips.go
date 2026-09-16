package tui

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	welcomeTipTypeInterval  = 50 * time.Millisecond
	welcomeTipHoldDuration  = 7 * time.Second
	welcomeTipEraseInterval = 50 * time.Millisecond
)

// Keep tips to one sentence and describe only shipped AICE behavior.
var welcomeTips = [...]string{
	"Run /login to connect your model provider.",
	"Use /model to switch models for the current provider.",
	"Use /thinking to adjust the reasoning level.",
	"Use /provider to switch model providers.",
	"Run /settings to see your current configuration.",
	"Type / to browse available commands.",
	"Press Tab to complete the selected slash command.",
	"Type @ to find and attach workspace files.",
	"Press Tab to confirm a file reference, then Enter to send.",
	"Attach an image with @ to ask a vision model about it.",
	"Press Ctrl+v or Alt+v to paste a clipboard image.",
	"Press Shift+Enter or Ctrl+j to insert a newline.",
	"Press Ctrl+g to edit a long prompt in your external editor.",
	"Press Enter while AICE works to add a correction.",
	"Press Ctrl+Enter while AICE works to queue a follow-up.",
	"Press Esc to stop the current response and keep your draft.",
	"Press Ctrl+c to clear your draft, then again to quit.",
	"Press Ctrl+o to expand or collapse execution details.",
	"Click Copy on a code block to copy the original code.",
	"Use /clear to clear the screen and keep Session history.",
	"Use /new to start a fresh Session with your next prompt.",
	"Run /tree to explore all branches of your Session.",
	"Use /checkout to choose where the next branch starts.",
	"Run /compact to summarize older context on this branch.",
	"Use /btw to ask a side question without tools.",
	"Run /skills to see the skills loaded for this Session.",
	"Use /browser to manage browser connections and tabs.",
	"Run /init to create or improve your project AGENTS.md.",
}

var welcomeTipControlPattern = regexp.MustCompile(
	`/[a-z]*|\b(?:(?:Ctrl|Alt|Shift)\+[A-Za-z]+|Tab|Enter|Esc)\b`,
)

type welcomeTipPhase uint8

const (
	welcomeTipTyping welcomeTipPhase = iota
	welcomeTipHolding
	welcomeTipErasing
)

// Mutated only by model.Update; rendering never advances time or randomness.
type welcomeTip struct {
	index   int
	text    []rune
	visible int
	phase   welcomeTipPhase
	nextAt  time.Time
}

func (tip *welcomeTip) selectNext() {
	var index int
	if len(tip.text) > 0 {
		// Choose directly from the other entries, without a retry loop.
		index = (tip.index + 1 + rand.IntN(len(welcomeTips)-1)) % len(welcomeTips)
	} else {
		index = rand.IntN(len(welcomeTips))
	}
	*tip = welcomeTip{index: index, text: []rune(welcomeTips[index])}
}

func (tip *welcomeTip) advance(now time.Time) {
	if len(tip.text) == 0 {
		tip.selectNext()
	}
	if tip.phase == welcomeTipHolding && tip.nextAt.IsZero() {
		// Give a fully visible tip its reading time again after a hidden view.
		tip.nextAt = now.Add(welcomeTipHoldDuration)
	}
	if now.Before(tip.nextAt) {
		return
	}
	switch tip.phase {
	case welcomeTipTyping:
		tip.visible++
		tip.nextAt = now.Add(welcomeTipTypeInterval)
		if tip.visible == len(tip.text) {
			tip.phase = welcomeTipHolding
			tip.nextAt = now.Add(welcomeTipHoldDuration)
		}
	case welcomeTipHolding:
		tip.phase = welcomeTipErasing
		tip.visible--
		tip.nextAt = now.Add(welcomeTipEraseInterval)
	case welcomeTipErasing:
		if tip.visible > 0 {
			tip.visible--
			tip.nextAt = now.Add(welcomeTipEraseInterval)
			return
		}
		tip.selectNext()
		tip.nextAt = now.Add(welcomeTipTypeInterval)
	}
}

func (m model) welcomeTipsVisible() bool {
	return len(m.entries) == 0 && !m.running && !m.side.isVisible && m.height >= 16
}

func (m model) welcomeTipsView() string {
	if !m.welcomeTipsVisible() {
		return ""
	}
	view := m.welcomeTip.view(m.viewport.Width())
	if lipgloss.Height(view)+3 > m.viewport.Height() {
		return ""
	}
	return view
}

func (tip welcomeTip) view(width int) string {
	width = max(width, 1)
	if len(tip.text) == 0 {
		return strings.Repeat(" ", width)
	}
	// Center each complete sentence (and each wrapped line) independently.
	// Reveal it in place so typing never pulls already visible text sideways.
	lines := strings.Split(ansi.Hardwrap(highlightWelcomeTip(string(tip.text)), width, true), "\n")
	remaining := tip.visible
	for i, line := range lines {
		runes := []rune(ansi.Strip(line))
		visible := min(max(remaining, 0), len(runes))
		left := (width - lipgloss.Width(line)) / 2
		text := string(runes[:visible])
		content := strings.Repeat(" ", left) + ansi.Cut(line, 0, lipgloss.Width(text))
		atCaret := remaining >= 0 && remaining < len(runes)
		atEnd := remaining == len(runes) && i == len(lines)-1
		if tip.phase != welcomeTipHolding && (atCaret || atEnd) && left+lipgloss.Width(text) < width {
			content += lipgloss.NewStyle().Foreground(secondaryColor).Render("▏")
		}
		lines[i] = lipgloss.NewStyle().Width(width).Render(content)
		remaining -= len(runes)
	}
	return strings.Join(lines, "\n")
}

// Style the complete sentence before wrapping and revealing it, so partial
// commands and shortcuts retain their color throughout typing and erasing.
func highlightWelcomeTip(text string) string {
	var content strings.Builder
	end := 0
	for _, match := range welcomeTipControlPattern.FindAllStringIndex(text, -1) {
		content.WriteString(mutedStyle.Render(text[end:match[0]]))
		content.WriteString(pathStyle.Render(text[match[0]:match[1]]))
		end = match[1]
	}
	content.WriteString(mutedStyle.Render(text[end:]))
	return content.String()
}
