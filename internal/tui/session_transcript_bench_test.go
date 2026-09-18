package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func BenchmarkHistoryMarkdownFirstView(b *testing.B) {
	for _, size := range []int{32 << 10, 128 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			unit := "A paragraph with **bold text**, `inline code` and 中文.\n\n- First item\n- Second item\n\n> A quotation.\n\n```go\nfunc answer() int { return 42 }\n```\n\n"
			source := strings.Repeat(unit, size/len(unit))
			view := &interaction.Transcript{SessionID: "benchmark", Entries: []interaction.TranscriptEntry{
				{Kind: interaction.TranscriptUser, ID: "question", Text: "Read the long answer"},
				{Kind: interaction.TranscriptAssistant, ID: "answer", Assistant: interaction.AssistantDisplay{Text: source, Concludes: true}},
			}}
			b.ReportAllocs()
			for b.Loop() {
				m := newModel(nil, nil)
				m.width, m.height = 120, 40
				m.resizeLayout()
				m.replaceTranscript(view)
				m.View()
			}
		})
	}
}
