package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Completed reasoning and tool bodies remain folded while their parent is
// expanded, as when reading a long tool run. Only the tail is on screen.
func BenchmarkFoldHistoryRefresh(b *testing.B) {
	for _, rounds := range []int{100, 1000} {
		b.Run(fmt.Sprint(rounds), func(b *testing.B) {
			m := newModel(nil, nil)
			m.width, m.height = 100, 32
			m.resizeLayout()
			id := m.beginProcess()
			output := strings.Repeat("source line\n", 1000)
			for range rounds {
				m.entries = append(m.entries,
					transcriptEntry{kind: entryAssistant, processID: id, thinking: "finished reasoning", complete: true},
					transcriptEntry{kind: entryTool, processID: id, toolName: "read", toolDetail: "/workspace/internal/example.go", toolDone: true,
						toolOutput: interaction.ToolOutputDisplay{Available: true, Text: output}},
				)
			}
			m.refreshViewport(true)
			b.ReportAllocs()
			for b.Loop() {
				m.refreshViewport(true)
				m.viewport.View()
			}
		})
	}
}
