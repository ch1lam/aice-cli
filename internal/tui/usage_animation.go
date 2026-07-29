package tui

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	usageAnimationInterval = 40 * time.Millisecond
	usageAnimationFrames   = 12
)

type usageAnimationTickMsg struct {
	generation uint64
}

type usageDisplay struct {
	inputTokens      int64
	outputTokens     int64
	cacheReadTokens  int64
	cacheWriteTokens int64
	totalCost        float64
}

type usageAnimation struct {
	current     usageDisplay
	start       usageDisplay
	target      usageDisplay
	frame       int
	generation  uint64
	initialized bool
	running     bool
}

func (a *usageAnimation) Start(initial, target llm.Usage) tea.Cmd {
	initialDisplay := newUsageDisplay(initial)
	if a.initialized {
		initialDisplay = a.current
	}

	a.current = initialDisplay
	a.start = initialDisplay
	a.target = newUsageDisplay(target)
	a.frame = 0
	a.generation++
	a.initialized = true

	if a.start == a.target {
		a.current = a.target
		a.running = false
		return nil
	}

	a.running = true
	return usageAnimationTick(a.generation)
}

func (a *usageAnimation) Update(message usageAnimationTickMsg) tea.Cmd {
	if !a.running || message.generation != a.generation {
		return nil
	}

	a.frame++
	if a.frame >= usageAnimationFrames {
		a.current = a.target
		a.running = false
		return nil
	}

	progress := float64(a.frame) / usageAnimationFrames
	a.current = interpolateUsageDisplay(
		a.start,
		a.target,
		easeOutCubic(progress),
	)
	return usageAnimationTick(a.generation)
}

func (a usageAnimation) Value(fallback llm.Usage) usageDisplay {
	if !a.initialized {
		return newUsageDisplay(fallback)
	}
	return a.current
}

func usageAnimationTick(generation uint64) tea.Cmd {
	return tea.Tick(usageAnimationInterval, func(time.Time) tea.Msg {
		return usageAnimationTickMsg{generation: generation}
	})
}

func newUsageDisplay(usage llm.Usage) usageDisplay {
	display := usageDisplay{
		inputTokens:      usage.InputTokens,
		outputTokens:     usage.OutputTokens,
		cacheReadTokens:  usage.CacheReadTokens,
		cacheWriteTokens: usage.CacheWriteTokens,
	}
	if usage.Cost != nil {
		display.totalCost = usage.Cost.Total
	}
	return display
}

func interpolateUsageDisplay(
	start,
	target usageDisplay,
	progress float64,
) usageDisplay {
	return usageDisplay{
		inputTokens: interpolateInt64(
			start.inputTokens,
			target.inputTokens,
			progress,
		),
		outputTokens: interpolateInt64(
			start.outputTokens,
			target.outputTokens,
			progress,
		),
		cacheReadTokens: interpolateInt64(
			start.cacheReadTokens,
			target.cacheReadTokens,
			progress,
		),
		cacheWriteTokens: interpolateInt64(
			start.cacheWriteTokens,
			target.cacheWriteTokens,
			progress,
		),
		totalCost: interpolateFloat(
			start.totalCost,
			target.totalCost,
			progress,
		),
	}
}

func interpolateInt64(start, target int64, progress float64) int64 {
	return start + int64(math.Round(float64(target-start)*progress))
}

func interpolateFloat(start, target, progress float64) float64 {
	return start + (target-start)*progress
}

func easeOutCubic(progress float64) float64 {
	remaining := 1 - progress
	return 1 - remaining*remaining*remaining
}
