package tui

import "github.com/ch1lam/aice-cli/internal/interaction"

// replaceTranscript installs completed presentation data without running the
// live event reducer (no execution, animation, or usage accounting).
func (m *model) replaceTranscript(view *interaction.Transcript) {
	m.sessionID = view.SessionID
	m.entries = nil
	m.processGroups = nil
	m.folds = nil
	m.selection.clear()
	m.pointer = transcriptPointer{}
	m.activeProcessID = 0
	m.assistantEntry = -1
	if view.ResetSideThreads {
		m.side = sidePanelState{manager: m.side.manager, threads: map[uint64]*sideThreadState{}}
	}
	for _, item := range view.Entries {
		switch item.Kind {
		case interaction.TranscriptUser:
			m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: item.Text, sourceID: item.ID})
			m.activeProcessID = 0
		case interaction.TranscriptAssistant:
			if m.activeProcessID == 0 {
				m.nextProcessID++
				m.activeProcessID = m.nextProcessID
				m.processGroups = append(m.processGroups, processGroup{id: m.activeProcessID, collapsed: true})
			}
			m.entries = append(m.entries, transcriptEntry{
				kind: entryAssistant, sourceID: item.ID, processID: m.activeProcessID,
				text: item.Assistant.Text, thinking: item.Assistant.Thinking,
				complete: true, conclusion: item.Assistant.Concludes, presentation: &assistantPresentation{},
			})
		case interaction.TranscriptTool:
			tool := item.Tool
			e := transcriptEntry{
				kind: entryTool, sourceID: item.ID, processID: m.activeProcessID,
				toolID: tool.ID, toolName: tool.Name, toolDetail: sanitizeToolDetail(tool.Detail, tool.Name == "bash"),
				toolDone: true, toolError: tool.Failed, toolOutput: tool.Output, toolDiff: tool.Diff, toolTruncation: tool.Truncation,
			}
			if tool.Name == "write" {
				e.writePreview = &writePreview{}
				e.writePreview.setContent(tool)
				e.toolPreviewRevision = e.writePreview.revision
			}
			m.entries = append(m.entries, e)
		case interaction.TranscriptNotice:
			m.entries = append(m.entries, transcriptEntry{kind: entryNotice, text: item.Text, sourceID: item.ID})
		}
	}
	m.activeProcessID = 0
	m.pendingDeliveries = nil
	m.refreshViewport(true)
}
