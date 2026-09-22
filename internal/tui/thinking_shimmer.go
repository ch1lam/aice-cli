package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Grok-style sweeping highlight for the live "Thinking..." activity row.
// Only the short status text shimmers; thinking bodies stay static muted.
const (
	thinkingShimmerBand   = 10
	thinkingShimmerStep   = 2
	thinkingShimmerLevels = 16
)

var thinkingShimmerBase = parseHexColor(mutedTextHex)
var thinkingShimmerPeak = parseHexColor("#FFFFFF")

var thinkingShimmerRGB = func() [][3]int {
	colors := make([][3]int, thinkingShimmerLevels)
	for level := range colors {
		progress := float64(level) / float64(thinkingShimmerLevels-1)
		mixed := interpolateColor(thinkingShimmerBase, thinkingShimmerPeak, progress)
		colors[level] = [3]int{
			int(math.Round(mixed[0])),
			int(math.Round(mixed[1])),
			int(math.Round(mixed[2])),
		}
	}
	return colors
}()

func thinkingShimmerSweepX(frame uint64, innerWidth int) int {
	cycle := innerWidth + thinkingShimmerBand*2
	if cycle <= 0 {
		return 0
	}
	return int(frame*thinkingShimmerStep%uint64(cycle)) - thinkingShimmerBand
}

// renderThinkingStatus shimmers one short status line (e.g. "Thinking...")
// with a light band sweeping left to right across its columns.
func renderThinkingStatus(status string, frame uint64) string {
	innerWidth := max(lipgloss.Width(status), 1)
	return shimmerThinkingLine(status, thinkingShimmerSweepX(frame, innerWidth))
}

func shimmerThinkingLine(line string, sweepX int) string {
	var builder strings.Builder
	x := 0
	current := -1
	for _, r := range line {
		cell := 1
		if r >= 0x1100 {
			cell = lipgloss.Width(string(r))
			if cell < 1 {
				cell = 1
			}
		}
		if r == ' ' || r == '\t' {
			builder.WriteRune(r)
			x += cell
			continue
		}
		distance := x - sweepX
		if distance < 0 {
			distance = -distance
		}
		level := 0
		if distance < thinkingShimmerBand {
			brightness := 1 - float64(distance)/float64(thinkingShimmerBand)
			brightness = brightness * brightness * (3 - 2*brightness)
			level = int(brightness*float64(thinkingShimmerLevels-1) + 0.5)
		}
		if level != current {
			rgb := thinkingShimmerRGB[level]
			fmt.Fprintf(&builder, "\x1b[38;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
			current = level
		}
		builder.WriteRune(r)
		x += cell
	}
	if current != -1 {
		builder.WriteString("\x1b[39m")
	}
	return builder.String()
}
