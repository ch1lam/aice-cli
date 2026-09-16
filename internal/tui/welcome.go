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

// The logo shares the ink theme palette. Closing the loop avoids a hard seam
// when the gradient wraps from cool stone blue back to warm sunset red.
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

// welcomeAnimation drives the gradient and brief signal glitches on the startup
// logo. It stops when a run starts or the first conversation entry appears.
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

// renderLogo keeps a fixed canvas even during tearing, so the version line and composer
// never move. Frames are pure projections: extra repaints cannot trigger glitches.
func (a welcomeAnimation) renderLogo() string {
	lines := strings.Split(welcomeLogo, "\n")
	rendered := make([]string, 0, len(lines))
	for row, line := range lines {
		cells := []rune(line)
		shift := a.glitchShift(row)
		var builder strings.Builder
		for column := range welcomeLogoWidth {
			character := welcomeCell(cells, column)
			accent := ""
			if shift != 0 {
				character, accent = a.glitchCell(cells, column, shift)
			}
			if character == ' ' {
				builder.WriteRune(character)
				continue
			}
			position := math.Mod(
				float64(column)/welcomeLogoWidth+
					float64(a.frame)*welcomeAnimationPhase,
				1.0,
			)
			if accent == "" {
				accent = formatHexColor(sampleWelcomeRamp(position))
			}
			builder.WriteString(
				lipgloss.NewStyle().
					Foreground(lipgloss.Color(accent)).
					Render(string(character)),
			)
		}
		rendered = append(rendered, builder.String())
	}
	return strings.Join(rendered, "\n")
}

// Unevenly spaced 300 ms bursts contain a clean frame between two jolts.
// Only one or two rows tear at a time, keeping the artwork recognizable.
func (a welcomeAnimation) glitchShift(row int) int {
	const cycle = 480 // 24 seconds at the existing 20 fps cadence.
	for burst, start := range [...]int{48, 137, 253, 391} {
		phase := a.frame%cycle - start
		if phase < 0 || phase >= 6 || phase == 2 {
			continue
		}
		band := (burst + a.frame/cycle) % welcomeLogoHeight
		if row != band && (phase < 3 || row != (band+1)%welcomeLogoHeight) {
			return 0
		}
		shift := 1 + (burst+phase)%3
		if (phase+row)%2 == 0 {
			shift = -shift
		}
		return shift
	}
	return 0
}

func (a welcomeAnimation) glitchCell(cells []rune, column, shift int) (rune, string) {
	character := welcomeCell(cells, column-shift)
	if character == ' ' {
		// Split channels leave a faint fringe on either side of the displaced row.
		if welcomeCell(cells, column-shift-1) != ' ' {
			return '░', sunsetHex
		}
		if welcomeCell(cells, column-shift+1) != ' ' {
			return '░', goldHex
		}
		return character, ""
	}
	// Sparse half-block dropouts suggest scan lines without blanking the logo.
	if (column+a.frame)%11 == 0 {
		return '▀', primaryTextHex
	}
	if (column/4+a.frame)%2 == 0 {
		return character, goldHex
	}
	return character, sunsetHex
}

func welcomeCell(cells []rune, column int) rune {
	if column < 0 || column >= len(cells) {
		return ' '
	}
	return cells[column]
}

// welcomeView renders the startup screen: the animated logo above
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
			"",
			card,
		)
		if lipgloss.Height(stacked) <= m.viewport.Height() {
			content = stacked
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
		BorderForeground(subtleColor)

	title := headerStyle.Render("✦  Ask AICE")
	description := mutedStyle.Render("Understand or change your code.")
	commandHint := mutedStyle.Render("Type / for commands.")
	if !m.apiKeyConfigured {
		title = headerStyle.Render("✦  Set up AICE")
		description = noticeStyle.Render("Sign in to a provider to start.")
		commandHint = mutedStyle.Render(
			"Use /login. View setup with /settings.",
		)
	}
	rows := []string{title, description, "", commandHint}
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
