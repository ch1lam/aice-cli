package tui

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func foldTestModel() model {
	m := newModel(nil, nil)
	m.width, m.height = 100, 32
	m.resizeLayout()
	id := m.beginProcess()
	m.entries = []transcriptEntry{
		{kind: entryAssistant, processID: id, thinking: "REASONING_BODY", text: "Inspecting files", complete: true},
		{kind: entryTool, processID: id, toolName: "skill", toolID: "skill-1", toolDetail: "golang-how-to", toolDone: true,
			toolOutput: interaction.ToolOutputDisplay{Text: "SKILL_BODY", Available: true}},
		{kind: entryTool, processID: id, toolName: "read", toolID: "read-1", toolDetail: "README.md", toolDone: true,
			toolOutput: interaction.ToolOutputDisplay{Text: "README_BODY", Available: true}},
		{kind: entryAssistant, processID: id, text: "FINAL_ANSWER", complete: true, conclusion: true},
	}
	m.refreshViewport(false)
	return m
}

func TestFoldHierarchyPreservesIndependentChildren(t *testing.T) {
	m := foldTestModel()
	view := ansi.Strip(m.transcriptView())
	for _, hidden := range []string{"REASONING_BODY", "SKILL_BODY", "README_BODY"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("default exposed %s", hidden)
		}
	}
	for _, shown := range []string{"Thinking", "1 skill · 1 file read", "README.md", "FINAL_ANSWER"} {
		if !strings.Contains(view, shown) {
			t.Fatalf("missing %s", shown)
		}
	}
	tool := foldTarget{kind: foldTool, id: 2}
	group := foldTarget{kind: foldCalls, id: 1}
	process := foldTarget{kind: foldProcess, id: 1}
	m.setFoldExpanded(tool, true)
	if view = ansi.Strip(m.transcriptView()); !strings.Contains(view, "README_BODY") || strings.Contains(view, "SKILL_BODY") {
		t.Fatal("individual tool expansion affected its sibling")
	}
	for _, parent := range []foldTarget{group, process} {
		m.setFoldExpanded(parent, false)
		view = ansi.Strip(m.transcriptView())
		if strings.Contains(view, "README_BODY") || !strings.Contains(view, "FINAL_ANSWER") {
			t.Fatal(view)
		}
		m.setFoldExpanded(parent, true)
		view = ansi.Strip(m.transcriptView())
		if !strings.Contains(view, "README_BODY") || strings.Contains(view, "SKILL_BODY") {
			t.Fatal("lost child choices")
		}
	}
	m.setFoldExpanded(foldTarget{kind: foldThinking, id: 0}, true)
	if !strings.Contains(m.transcriptView(), "REASONING_BODY") {
		t.Fatal("thinking did not expand")
	}
	m.resetBranchTranscript()
	if len(m.folds) != 0 {
		t.Fatal("branch reset retained stale fold targets")
	}
}

func TestManualFoldsSurviveStreamingConclusionAndNewCalls(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "open"}[expanded], func(t *testing.T) {
			m := foldTestModel()
			m.running, m.assistantEntry = true, 3
			target := foldTarget{kind: foldProcess, id: 1}
			m.setFoldExpanded(target, expanded)
			m.markConclusion()
			m.revokeConclusion()
			if m.foldExpanded(target) != expanded {
				t.Fatal("stream overwrote manual choice")
			}
		})
	}
	m := foldTestModel()
	m.setFoldExpanded(foldTarget{kind: foldCalls, id: 1}, false)
	m.entries = append(m.entries[:3], transcriptEntry{kind: entryTool, processID: 1, toolName: "read", toolDetail: "NEW_FILE"})
	view := m.transcriptView()
	if strings.Contains(view, "NEW_FILE") || !strings.Contains(view, "2 file reads") {
		t.Fatal("new sibling reset folded group")
	}
}

func TestFoldedResultsUpdateWithoutFormattingHiddenBody(t *testing.T) {
	m := foldTestModel()
	m.entries[2].toolDone = false
	m.completeTool(ToolDisplay{ID: "read-1", Output: interaction.ToolOutputDisplay{Text: "LATEST_RESULT", Available: true}})
	m.refreshViewport(false)
	if strings.Contains(m.viewport.GetContent(), "LATEST_RESULT") {
		t.Fatal("folded result leaked")
	}
	m.setFoldExpanded(foldTarget{kind: foldTool, id: 2}, true)
	m.refreshViewport(false)
	if !strings.Contains(m.viewport.GetContent(), "LATEST_RESULT") {
		t.Fatal("expanded result stale")
	}
}
