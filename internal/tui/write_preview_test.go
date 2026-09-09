package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestWritePreviewStreamAndExecution(t *testing.T) {
	m := updateModel(t, newModel(nil, nil), tea.WindowSizeMsg{Width: 80, Height: 30})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaToolCall, ToolIndex: 2, Tool: ToolDisplay{ID: "write-1", Name: "write"}}})
	raw := `{"path":"literal~@.go","content":"package main\n\n// 中文\n`
	for _, fragment := range []string{raw[:20], raw[20:]} {
		m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaToolCall, ToolIndex: 2, Arguments: fragment}})
	}
	view := ansi.Strip(m.transcriptView())
	if !strings.Contains(view, "package main") || !strings.Contains(view, "not executed") || !strings.Contains(view, "中文") {
		t.Fatalf("partial preview: %s", view)
	}
	tool := ToolDisplay{ID: "write-1", Name: "write", Detail: "literal~@.go", Content: "package main\n", HasContent: true}
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: tool})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{ID: tool.ID, Failed: true}})
	if len(m.entries) != 2 || !m.entries[1].toolError || m.entries[1].toolPreparing {
		t.Fatalf("execution did not reconcile preview: %+v", m.entries)
	}
	if !strings.Contains(ansi.Strip(m.transcriptView()), "literal~@.go") {
		t.Fatal("literal path lost")
	}
}

func TestWritePreviewBoundsAndExpand(t *testing.T) {
	m := updateModel(t, newModel(nil, nil), tea.WindowSizeMsg{Width: 80, Height: 30})
	var content strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&content, "line%02d\n", i)
	}
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "a", Name: "write", Content: content.String(), HasContent: true}})
	view := ansi.Strip(m.transcriptView())
	if !strings.Contains(view, "line10") || strings.Contains(view, "line11") {
		t.Fatalf("default bound: %s", view)
	}
	for range 2 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		m = updated.(model)
	}
	view = ansi.Strip(m.transcriptView())
	if !strings.Contains(view, "line20") {
		t.Fatalf("Ctrl+O expansion: %s", view)
	}
	p := &writePreview{}
	p.setContent(ToolDisplay{Content: strings.Repeat("界", maximumWritePreviewBytes), HasContent: true})
	if len(p.content) > maximumWritePreviewBytes || !p.truncated {
		t.Fatal("unbounded stored content")
	}
	view = ansi.Strip(p.view(35, false))
	if len(view) > 1000 {
		t.Fatal("long source line was not clipped")
	}
}

func TestWritePreviewEscapesAndControls(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"unfinished escape", `"abc\`, "abc"},
		{"unfinished unicode", `"abc\u12`, "abc"},
		{"unicode pair", `"\ud83d\ude00!`, "😀!"},
		{"escaped quote", `"a\"b`, "a\"b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := partialJSONString(tc.raw)
			if !ok || got != tc.want {
				t.Fatalf("got %q %v", got, ok)
			}
		})
	}
	p := &writePreview{}
	p.setContent(ToolDisplay{Content: "```\n\x1b]52;c;PAYLOAD\a\x9b\x00\n```", HasContent: true})
	view := p.view(80, false)
	if strings.Contains(view, "\x1b]52") || strings.ContainsAny(ansi.Strip(view), "\a\x00\u009b") {
		t.Fatalf("terminal control escaped: %q", view)
	}
}

func TestWritePreviewLargeStreamStopsGrowing(t *testing.T) {
	m := updateModel(t, newModel(nil, nil), tea.WindowSizeMsg{Width: 80, Height: 30})
	m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
	delta := DisplayDelta{Kind: DisplayDeltaToolCall, Tool: ToolDisplay{Name: "write", ID: "a"}, Arguments: strings.Repeat("x", maximumWritePreviewBytes+100)}
	m.applyAssistantDelta(DisplayEvent{Delta: delta})
	p := m.entries[1].writePreview
	revision := p.revision
	for range 100 {
		m.applyAssistantDelta(DisplayEvent{Delta: delta})
	}
	if p.raw.Len() != maximumWritePreviewBytes || p.revision != revision {
		t.Fatal("saturated stream keeps rebuilding")
	}
}

func TestPartialWriteFieldsEveryBoundary(t *testing.T) {
	content := "中文\nquote \" slash \\ emoji 😀"
	raw, _ := json.Marshal(map[string]string{"content": content, "path": "test.go"})
	for i := 1; i <= len(raw); i++ {
		_, got, known := partialWriteFields(string(raw[:i]))
		if known && !strings.HasPrefix(content, got) {
			t.Fatalf("offset %d: %q is not a prefix", i, got)
		}
	}
}

func TestWritePreviewCallsRemainIndependent(t *testing.T) {
	m := updateModel(t, newModel(nil, nil), tea.WindowSizeMsg{Width: 80, Height: 30})
	for round := range 2 {
		m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
		for index := range 2 {
			id := fmt.Sprintf("round%d-call%d", round, index)
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantDelta, Delta: DisplayDelta{Kind: DisplayDeltaToolCall, ToolIndex: index, Tool: ToolDisplay{ID: id, Name: "write"}, Arguments: `{"content":"` + id}})
		}
		m.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantEnd})
	}
	for _, e := range m.entries {
		if e.kind != entryTool {
			continue
		}
		if !e.toolPreparing || e.toolDone {
			t.Fatal("preview claimed execution without execution events")
		}
		if !strings.Contains(ansi.Strip(e.writePreview.view(80, false)), e.toolID) {
			t.Fatal("stream crossed tool or round boundary")
		}
	}
}

func BenchmarkWritePreviewVisiblePrefix(b *testing.B) {
	for _, size := range []int{4096, 4 * 1024 * 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			p := &writePreview{}
			p.setContent(ToolDisplay{Content: strings.Repeat("package main\n", size/13), Detail: "file.go", HasContent: true})
			p.view(80, false)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.revision++
				p.view(80, false)
			}
		})
	}
}
