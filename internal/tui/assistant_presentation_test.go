package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLongThinkingPreviewPreservesCompleteMessage(t *testing.T) {
	for _, side := range []bool{false, true} {
		name := "main"
		if side {
			name = "side"
		}
		t.Run(name, func(t *testing.T) {
			m := newModel(nil, nil)
			m.width, m.height, m.running = 80, 24, true
			thread := &sideThreadState{entries: []sideThreadEntry{{}}, assistantEntry: 0, isRunning: true}
			apply := func(event DisplayEvent) {
				if side {
					m.applySideEvent(thread, event)
				} else {
					m.applyAgentEvent(event)
				}
			}
			view := func() string {
				if side {
					return ansi.Strip(m.sideAnswerView(thread.entries[0], true))
				}
				return ansi.Strip(m.transcriptView())
			}
			apply(DisplayEvent{Kind: DisplayEventAssistantStart})
			prefix := "BEGINNING " + strings.Repeat("中文推理🙂 ", 6000)
			apply(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: prefix}})
			apply(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: " LATEST"}})
			preview := view()
			if !utf8.ValidString(preview) || !strings.Contains(preview, "LATEST") ||
				!strings.Contains(preview, liveThinkingNotice) || strings.Contains(preview, "BEGINNING") {
				t.Fatal("live preview must show a valid UTF-8 tail and an omission notice")
			}
			if len(preview) > 2*maximumLiveThinkingBytes {
				t.Fatalf("live preview grew to %d bytes", len(preview))
			}
			full := prefix + " LATEST"
			retained := m.entries
			if side {
				if thread.entries[0].thinking != full {
					t.Fatal("side thinking was truncated in storage")
				}
			} else if retained[0].thinking != full {
				t.Fatal("main thinking was truncated in storage")
			}
			apply(DisplayEvent{Kind: DisplayEventAssistantEnd, Assistant: AssistantDisplay{Thinking: full}})
			completed := view()
			if !strings.Contains(completed, "BEGINNING") || !strings.Contains(completed, "LATEST") ||
				strings.Contains(completed, liveThinkingNotice) {
				t.Fatal("completed expanded thinking must show the full message")
			}
		})
	}
}

func TestAssistantPresentationInvalidation(t *testing.T) {
	p := &assistantPresentation{}
	text := p.appendText("", "first")
	before := p.textView(text, 80)
	text = p.appendText(text, " second")
	if got := p.textView(text, 80); got == before || !strings.Contains(ansi.Strip(got), "second") {
		t.Fatal("new text did not invalidate cached content")
	}
	text = p.appendText(text, strings.Repeat(" word", 30))
	wide := p.textView(text, 80)
	narrow := p.textView(text, 30)
	if strings.Count(narrow, "\n") <= strings.Count(wide, "\n") {
		t.Fatal("resize did not rewrap cached content")
	}
	if p.textView("", 30) != "" {
		t.Fatal("empty replacement retained old content")
	}
	// An appended delta must not mutate an earlier immutable snapshot.
	if !strings.HasPrefix(text, "first second") {
		t.Fatal("stream accumulation lost content")
	}
	snapshot := p.appendThinking("", "snapshot")
	p.appendThinking(snapshot, " extension")
	if snapshot != "snapshot" {
		t.Fatal("append mutated a published snapshot")
	}
}

func TestCollapsedProcessSkipsAssistantRendering(t *testing.T) {
	m := newModel(nil, nil)
	id := m.beginProcess()
	m.processGroups[0].collapsed = true
	p := &assistantPresentation{}
	m.entries = []transcriptEntry{{kind: entryAssistant, processID: id,
		thinking: strings.Repeat("hidden ", 20000), text: "hidden text", presentation: p}}
	view := m.transcriptView()
	if strings.Contains(view, "hidden") || p.textCache.rendered != "" || p.thinkingCache.rendered != "" {
		t.Fatal("collapsed process rendered hidden content")
	}
	m.entries[0].conclusion = true
	m.entries[0].text = "visible answer"
	if !strings.Contains(ansi.Strip(m.transcriptView()), "visible answer") || p.thinkingCache.rendered != "" {
		t.Fatal("collapsed process must render the conclusion without rendering thinking")
	}
}

func TestLongThinkingStillAllowsInputAndCancellation(t *testing.T) {
	m := newModel(nil, nil)
	m.width, m.height, m.running, m.acceptsDelivery = 80, 24, true, true
	m.resizeLayout()
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{
		Kind: DisplayDeltaThinking, Delta: strings.Repeat("reasoning ", 100000),
	}})
	m.refreshViewport(true)
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.input.Value() != "x" {
		t.Fatal("long thinking consumed composer input")
	}
	cancelled := false
	m.cancelRun = func() { cancelled = true }
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !cancelled {
		t.Fatal("long thinking prevented cancellation")
	}
}
