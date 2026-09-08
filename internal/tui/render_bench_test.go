package tui

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkThinkingRefresh(b *testing.B) {
	for _, size := range []int{1024, 131072, 1048576} {
		for _, collapsed := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/collapsed=%v", size, collapsed), func(b *testing.B) {
				m := newModel(nil, nil)
				m.width = 120
				m.height = 40
				m.running = true
				m.resizeLayout()
				id := m.beginProcess()
				m.processGroups[0].collapsed = collapsed
				m.entries = []transcriptEntry{{kind: entryAssistant, processID: id, thinking: strings.Repeat("Considering the implementation and verification. ", size/48)}}
				m.assistantEntry = 0
				m.refreshViewport(true)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					m.applyAssistantDelta(DisplayEvent{Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: "."}})
					m.refreshViewport(true)
				}
			})
		}
	}
}
