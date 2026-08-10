package tui

import (
	"fmt"
	"time"
)

func formatRunDuration(duration time.Duration) string {
	duration = max(duration, 0)

	switch {
	case duration < time.Second:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	case duration < time.Minute:
		return fmt.Sprintf("%ds", duration/time.Second)
	case duration < time.Hour:
		minutes := duration / time.Minute
		seconds := duration % time.Minute / time.Second
		return fmt.Sprintf("%dmin %ds", minutes, seconds)
	default:
		hours := duration / time.Hour
		minutes := duration % time.Hour / time.Minute
		return fmt.Sprintf("%dh %dmin", hours, minutes)
	}
}

func (m *model) updateActiveProcessDuration(now time.Time) bool {
	group := m.processGroup(m.activeProcessID)
	if group == nil || group.startedAt.IsZero() {
		return false
	}

	previous := formatRunDuration(group.elapsed)
	group.elapsed = max(now.Sub(group.startedAt), 0)
	return formatRunDuration(group.elapsed) != previous
}

func (m model) processDuration(processID int) (time.Duration, bool) {
	for _, group := range m.processGroups {
		if group.id == processID && !group.startedAt.IsZero() {
			return group.elapsed, true
		}
	}
	return 0, false
}
