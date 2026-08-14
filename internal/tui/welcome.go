package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	welcomeAnimationInterval = 50 * time.Millisecond
	// welcomeAnimationPhase is how far the logo gradient sweeps each frame,
	// expressed as a fraction of one pass over the palette loop.
	welcomeAnimationPhase = 0.008
	welcomeLogoWidth      = 38
	welcomeLogoHeight     = 5
)

// welcomeRamp is a closed loop through the ink palette, warm red to cool blue
// and back, so the animated logo cycles without a hard seam where it wraps.
var welcomeRamp = []string{
	sunsetHex,
	warningHex,
	goldHex,
	successHex,
	informationHex,
}

var welcomeRampRGB = func() [][3]float64 {
	colors := make([][3]float64, len(welcomeRamp))
	for index, hex := range welcomeRamp {
		colors[index] = parseHexColor(hex)
	}
	return colors
}()

// welcomeLogo is a five-row block rendering of the AICE name. Every character
// is a single terminal cell wide, so per-cell color positions line up.
const welcomeLogo = ` ██████   ████████   ███████  ████████
██    ██     ██     ██        ██
████████     ██     ██        ███████
██    ██     ██     ██        ██
██    ██  ████████   ███████  ████████`

// welcomeTagline reads as a product line beneath the logo, distinct from the
// welcome card's call to action.
const welcomeTagline = "your codebase agent"

type welcomeTickMsg struct {
	generation uint64
}

type welcomeUpdateState uint8

const (
	welcomeUpdateUnknown welcomeUpdateState = iota
	welcomeUpdateChecking
	welcomeUpdateDisabled
	welcomeUpdateDevelopment
	welcomeUpdateCurrent
	welcomeUpdateAvailable
	welcomeUpdateFailed
)

type welcomeUpdateStatus struct {
	state  welcomeUpdateState
	latest string
}

type updateCheckMsg struct {
	result UpdateCheckResult
	err    error
}

// welcomeAnimation drives the gradient sweep across the startup logo. It ticks
// only while the welcome screen is actually visible and stops as soon as the
// first conversation entry appears.
type welcomeAnimation struct {
	frame      int
	generation uint64
	running    bool
}

func (a *welcomeAnimation) Start() tea.Cmd {
	if a.running {
		return nil
	}
	a.generation++
	a.running = true
	return welcomeTick(a.generation)
}

func (a *welcomeAnimation) Update(message welcomeTickMsg, active bool) tea.Cmd {
	if !a.running || message.generation != a.generation {
		return nil
	}
	if !active {
		a.running = false
		return nil
	}
	a.frame++
	return welcomeTick(a.generation)
}

// tick emits one tick for the animation's current generation. Init uses it to
// start the loop after newModel seeds the animation as already running.
func (a welcomeAnimation) tick() tea.Cmd {
	return welcomeTick(a.generation)
}

func welcomeTick(generation uint64) tea.Cmd {
	return tea.Tick(welcomeAnimationInterval, func(time.Time) tea.Msg {
		return welcomeTickMsg{generation: generation}
	})
}

// renderLogo colors every block of the logo from the animated ramp: columns
// are spread across the palette and the whole sweep advances with the frame,
// producing a slow moving gradient.
func (a welcomeAnimation) renderLogo() string {
	lines := strings.Split(welcomeLogo, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		// Pad each row so the block letters stay aligned even if the source
		// literal lost trailing whitespace.
		if width := lipgloss.Width(line); width < welcomeLogoWidth {
			line += strings.Repeat(" ", welcomeLogoWidth-width)
		}
		var builder strings.Builder
		column := 0
		for _, character := range line {
			if character == ' ' {
				builder.WriteRune(character)
				column++
				continue
			}
			position := math.Mod(
				float64(column)/welcomeLogoWidth+
					float64(a.frame)*welcomeAnimationPhase,
				1.0,
			)
			color := sampleWelcomeRamp(position)
			builder.WriteString(
				lipgloss.NewStyle().
					Foreground(lipgloss.Color(formatHexColor(color))).
					Render(string(character)),
			)
			column++
		}
		rendered = append(rendered, builder.String())
	}
	return strings.Join(rendered, "\n")
}

// welcomeView renders the startup screen: the animated logo and tagline above
// the contextual welcome card. On terminals too small for the logo it falls
// back to the card alone, preserving the pre-logo behavior.
func (m model) welcomeView() string {
	card := m.welcomeCard()

	content := card
	logo := m.welcomeAnimation.renderLogo()
	if lipgloss.Width(logo) <= m.viewport.Width() {
		stacked := lipgloss.JoinVertical(
			lipgloss.Center,
			logo,
			mutedStyle.Render(welcomeTagline),
			card,
		)
		if lipgloss.Height(stacked) <= m.viewport.Height() {
			content = stacked
		} else if compact := lipgloss.JoinVertical(
			lipgloss.Center,
			logo,
			card,
		); lipgloss.Height(compact) <= m.viewport.Height() {
			// Drop the tagline before the logo on shorter terminals.
			content = compact
		}
	}

	return lipgloss.Place(
		m.viewport.Width(),
		m.viewport.Height(),
		lipgloss.Center,
		lipgloss.Center,
		content,
	)
}

// welcomeCard is the contextual panel: what to do next, or how to configure
// AICE when no provider credential is set.
func (m model) welcomeCard() string {
	width := max(m.viewport.Width()-8, 20)
	width = min(width, 62)
	cardStyle := lipgloss.NewStyle().
		Width(max(width-6, 1)).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Background(panelBlackColor)

	title := headerStyle.Render("✦  Work with your codebase")
	description := mutedStyle.Render(
		"Ask AICE to trace behavior, explain architecture, or inspect a file.",
	)
	commandHint := mutedStyle.Render("Type / to browse interactive commands.")
	if !m.apiKeyConfigured {
		title = headerStyle.Render("✦  Configure AICE")
		description = noticeStyle.Render(
			"No provider API key is configured.",
		)
		commandHint = mutedStyle.Render(
			"Run /login to add one, or /settings to inspect configuration.",
		)
	}
	toolLabel := mutedStyle.Render("AVAILABLE TOOLS")
	tools := labelStyle.Render("read   ls   grep   find")
	rows := []string{title, description, commandHint, "", toolLabel, tools}
	versionStatus := make([]string, 0, 2)
	if m.version != "" {
		versionStatus = append(versionStatus, mutedStyle.Render(m.version))
	}
	if status := m.welcomeUpdateView(); status != "" {
		versionStatus = append(versionStatus, status)
	}
	if len(versionStatus) > 0 {
		rows = append(rows, "", strings.Join(versionStatus, "  "))
	}
	return cardStyle.Render(strings.Join(rows, "\n"))
}

func (m model) welcomeUpdateView() string {
	switch m.welcomeUpdate.state {
	case welcomeUpdateChecking:
		return infoStyle.Render("◌") + " " + mutedStyle.Render("Checking for updates...")
	case welcomeUpdateDisabled:
		return mutedStyle.Render("Update checks disabled")
	case welcomeUpdateDevelopment:
		return mutedStyle.Render("Development build · update check skipped")
	case welcomeUpdateCurrent:
		return lipgloss.NewStyle().
			Foreground(successColor).
			Render("✓ You're on the latest version")
	case welcomeUpdateAvailable:
		latest := sanitizeToolDetail(m.welcomeUpdate.latest, false)
		return noticeStyle.Render(
			"↑ " + latest + " available · run `aice update`",
		)
	case welcomeUpdateFailed:
		return mutedStyle.Render("Couldn't check for updates")
	default:
		return ""
	}
}

// parseHexColor converts "#RRGGBB" into normalized RGB components in [0, 255].
func parseHexColor(hex string) [3]float64 {
	value, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return [3]float64{
		float64((value >> 16) & 0xFF),
		float64((value >> 8) & 0xFF),
		float64(value & 0xFF),
	}
}

func formatHexColor(color [3]float64) string {
	return fmt.Sprintf("#%02X%02X%02X",
		int(math.Round(color[0])),
		int(math.Round(color[1])),
		int(math.Round(color[2])),
	)
}

func interpolateColor(a, b [3]float64, progress float64) [3]float64 {
	return [3]float64{
		a[0] + (b[0]-a[0])*progress,
		a[1] + (b[1]-a[1])*progress,
		a[2] + (b[2]-a[2])*progress,
	}
}

// sampleWelcomeRamp returns the palette color at position along the closed
// loop, with position normalized to [0, 1). The last ramp entry blends back
// into the first so the loop has no visible seam.
func sampleWelcomeRamp(position float64) [3]float64 {
	position = math.Mod(position, 1.0)
	if position < 0 {
		position += 1.0
	}
	scaled := position * float64(len(welcomeRampRGB))
	index := int(scaled)
	if index >= len(welcomeRampRGB) {
		index = len(welcomeRampRGB) - 1
	}
	next := (index + 1) % len(welcomeRampRGB)
	return interpolateColor(
		welcomeRampRGB[index],
		welcomeRampRGB[next],
		scaled-math.Floor(scaled),
	)
}
