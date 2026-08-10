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
