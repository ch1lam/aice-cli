package tui

import (
	"github.com/ch1lam/aice-cli/internal/interaction"
	"strings"
	"testing"
)

func TestToolTruncationStatusInTranscript(t *testing.T) {
	for _, tt := range []struct {
		name    string
		details interaction.TruncationDisplay
		want    string
	}{
		{name: "legacy", want: ""},
		{name: "grep", details: interaction.TruncationDisplay{Reason: "100 matches limit", OutputLines: 100, OutputBytes: 2000, Hint: "increase limit; refine pattern or path"}, want: "Truncated (100 matches limit): 100 lines, 2000 bytes; increase limit; refine pattern or path"},
		{name: "unknown total", details: interaction.TruncationDisplay{Reason: "50 KiB limit", OutputLines: 49, OutputBytes: 50176, NextOffset: 50}, want: "Truncated (50 KiB limit): 49 lines, 50176 bytes; total lines unknown; continue at offset=50"},
		{name: "known total", details: interaction.TruncationDisplay{Reason: "requested line limit", OutputLines: 1, OutputBytes: 4, NextOffset: 2, TotalLines: 3, TotalLinesKnown: true}, want: "3 total lines; continue at offset=2"},
		{name: "oversized", details: interaction.TruncationDisplay{Reason: "line exceeds output budget", NextOffset: 1, RequiresBash: true}, want: "0 lines, 0 bytes; total lines unknown; offset=1 unchanged; use bash"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(nil, nil)
			m.width = 160
			m.height = 30
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "read-1", Name: "read", Detail: "file.txt"}})
			m.expandAllDetails(true)
			// Populate the viewport cache before tool completion to catch stale status rows.
			m.refreshViewport(true)
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{ID: "read-1", Truncation: tt.details}})
			m.refreshViewport(true)
			view := m.transcriptView()
			if tt.details.Hint != "" && (strings.Contains(view, "offset=") || strings.Contains(view, "total lines")) {
				t.Fatalf("search displayed read pagination: %s", view)
			}
			if tt.want == "" {
				if strings.Contains(view, "Truncated") {
					t.Fatal(view)
				}
			} else if !strings.Contains(view, tt.want) {
				t.Fatalf("missing %q in %s", tt.want, view)
			}
		})
	}
}
