package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func BenchmarkHistoryNavigation(b *testing.B) {
	for _, test := range []struct {
		name, source string
		turns        int
	}{
		{"many-turns", strings.Repeat("A paragraph with **bold** and `code`.\n\n", 30), 500},
		{"long-paragraph", strings.Repeat("A paragraph with **bold** and `code`. ", 3600), 1},
		{"long-list", strings.Repeat("- A list item with **bold** and `code`.\n", 1000), 1},
	} {
		view := &interaction.Transcript{SessionID: "navigation"}
		for i := range test.turns {
			view.Entries = append(view.Entries,
				interaction.TranscriptEntry{Kind: interaction.TranscriptUser, ID: fmt.Sprint(i), Text: "Explain the implementation"},
				interaction.TranscriptEntry{Kind: interaction.TranscriptAssistant, Assistant: interaction.AssistantDisplay{Text: test.source, Concludes: true}})
		}
		b.Run(test.name+"/first-view", func(b *testing.B) {
			for b.Loop() {
				m := newModel(nil, nil)
				m.width, m.height = 120, 40
				m.resizeLayout()
				m.replaceTranscript(view)
				m.View()
			}
		})
		b.Run(test.name+"/scroll", func(b *testing.B) {
			m := newModel(nil, nil)
			m.width, m.height = 120, 40
			m.resizeLayout()
			m.replaceTranscript(view)
			for b.Loop() {
				next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
				m = next.(model)
				m.View()
				if m.viewport.index == 0 && m.viewport.part == 0 && m.viewport.line == 0 {
					m.viewport.GotoBottom()
				}
			}
		})
	}
}
