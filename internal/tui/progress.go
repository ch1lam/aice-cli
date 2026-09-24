package tui

import "charm.land/bubbles/v2/progress"

// NewDownloadProgress applies the ink palette to command-line downloads.
func NewDownloadProgress(width int) progress.Model {
	bar := progress.New(
		progress.WithWidth(width),
		progress.WithColors(accentColor, secondaryColor),
		progress.WithScaled(true),
	)
	bar.EmptyColor = subtleColor
	bar.PercentageStyle = mutedStyle
	return bar
}
