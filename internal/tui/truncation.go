package tui

import (
	"fmt"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func toolTruncationStatus(t interaction.TruncationDisplay) string {
	total := "total lines unknown"
	if t.TotalLinesKnown {
		total = fmt.Sprintf("%d total lines", t.TotalLines)
	}
	next := fmt.Sprintf("continue at offset=%d", t.NextOffset)
	if t.RequiresBash {
		next = fmt.Sprintf("offset=%d unchanged; use bash to read this line", t.NextOffset)
	}
	return fmt.Sprintf("Truncated (%s): %d lines, %d bytes; %s; %s",
		t.Reason, t.OutputLines, t.OutputBytes, total, next)
}
