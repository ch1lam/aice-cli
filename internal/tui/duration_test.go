package tui

import (
	"testing"
	"time"
)

func TestFormatRunDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{name: "negative", duration: -time.Second, expected: "0ms"},
		{name: "milliseconds", duration: 999 * time.Millisecond, expected: "999ms"},
		{name: "seconds", duration: time.Second, expected: "1s"},
		{
			name:     "seconds truncate subsecond",
			duration: 59*time.Second + 999*time.Millisecond,
			expected: "59s",
		},
		{name: "minutes and seconds", duration: time.Minute, expected: "1min 0s"},
		{
			name: "minutes and seconds truncate subsecond",
			duration: 59*time.Minute +
				42*time.Second +
				999*time.Millisecond,
			expected: "59min 42s",
		},
		{name: "hours and minutes", duration: time.Hour, expected: "1h 0min"},
		{
			name: "hours and minutes omit seconds",
			duration: 25*time.Hour +
				17*time.Minute +
				42*time.Second,
			expected: "25h 17min",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRunDuration(tt.duration); got != tt.expected {
				t.Errorf(
					"formatRunDuration(%s) = %q, want %q",
					tt.duration,
					got,
					tt.expected,
				)
			}
		})
	}
}
