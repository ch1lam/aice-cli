package tui

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	usageAnimationInterval = 40 * time.Millisecond
	usageAnimationFrames   = 12
)

type usageAnimationTickMsg struct {
	generation uint64
}

type usageAnimation struct {
	current     DisplayUsage
	start       DisplayUsage
	target      DisplayUsage
	frame       int
	generation  uint64
	initialized bool
	running     bool
}

func (a *usageAnimation) Start(initial, target DisplayUsage) tea.Cmd {
	initialDisplay := initial
	if a.initialized {
		initialDisplay = a.current
	}

	a.current = initialDisplay
	a.start = initialDisplay
	a.target = target
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

func (a usageAnimation) Value(fallback DisplayUsage) DisplayUsage {
	if !a.initialized {
		return fallback
	}
	return a.current
}

func usageAnimationTick(generation uint64) tea.Cmd {
	return tea.Tick(usageAnimationInterval, func(time.Time) tea.Msg {
		return usageAnimationTickMsg{generation: generation}
	})
}

func interpolateUsageDisplay(
	start,
	target DisplayUsage,
	progress float64,
) DisplayUsage {
	return DisplayUsage{
		InputTokens: interpolateInt64(
			start.InputTokens,
			target.InputTokens,
			progress,
		),
		OutputTokens: interpolateInt64(
			start.OutputTokens,
			target.OutputTokens,
			progress,
		),
		CacheReadTokens: interpolateInt64(
			start.CacheReadTokens,
			target.CacheReadTokens,
			progress,
		),
		CacheWriteTokens: interpolateInt64(
			start.CacheWriteTokens,
			target.CacheWriteTokens,
			progress,
		),
		TotalCost: interpolateFloat(
			start.TotalCost,
			target.TotalCost,
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
