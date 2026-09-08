package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
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

func BenchmarkGuardWithLongTranscript(b *testing.B) {
	m := newModel(nil, nil)
	m.width, m.height, m.running = 190, 40, true
	m.resizeLayout()
	id := m.beginProcess()
	for range 50 {
		m.entries = append(m.entries, transcriptEntry{
			kind: entryAssistant, processID: id, complete: true,
			thinking:     strings.Repeat("Considering the implementation and verification. ", 100),
			presentation: &assistantPresentation{},
		})
	}
	m.refreshViewport(true)
	m.guardPending = &interaction.GuardRequest{Path: "/outside", Options: guardTestOptions()}
	m.resizeLayout()
	b.Run("view", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.View()
		}
	})
	b.Run("refresh", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.refreshViewport(false)
		}
	})
}

func BenchmarkLongConversation(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := newModel(nil, nil)
			m.width, m.height, m.running = 120, 40, true
			m.resizeLayout()
			id := m.beginProcess()
			for range count {
				m.entries = append(m.entries, transcriptEntry{kind: entryAssistant, processID: id, complete: true,
					thinking: strings.Repeat("Already considered implementation and verification. ", 80), presentation: &assistantPresentation{}})
			}
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
			m.refreshViewport(true)
			b.Run("draw", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					m.View()
				}
			})
			b.Run("stream", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					m.applyAssistantDelta(DisplayEvent{Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: "."}})
					m.refreshViewport(true)
					m.View()
				}
			})
		})
	}
}
