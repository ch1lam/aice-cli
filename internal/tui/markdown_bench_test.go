package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Each operation updates a bounded tail after a fixed prefix. Alternating the
// tail avoids measuring an exact-source cache hit or growing the fixture with N.
func BenchmarkMarkdownRefresh(b *testing.B) {
	for _, size := range []int{1024, 8192, 32768} {
		for _, kind := range []string{"prose", "mixed", "open-code"} {
			b.Run(fmt.Sprintf("%s/%d", kind, size), func(b *testing.B) {
				unit := "A paragraph with **bold** and `inline code`.\n\n"
				if kind == "mixed" {
					unit += "```go\nfunc answer() int { return 42 }\n```\n\n"
				}
				prefix := strings.Repeat(unit, max(size/len(unit), 1))
				if kind == "open-code" {
					prefix = "```go\n" + strings.Repeat("var answer = 42\n", size/16)
				}
				sources := [2]string{prefix + "next", prefix + "next."}
				m := newModel(nil, nil)
				m.width, m.height, m.running = 120, 40, true
				m.resizeLayout()
				m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
				m.applyAssistantDelta(DisplayEvent{Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: sources[0]}})
				m.refreshViewport(true)
				i := 0
				b.ReportAllocs()
				for b.Loop() {
					i ^= 1
					m.entries[m.assistantEntry].text = sources[i]
					m.refreshViewport(true)
					m.View()
				}
			})
		}
	}
}
