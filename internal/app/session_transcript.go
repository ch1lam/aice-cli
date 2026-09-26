package app

import (
	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// sessionTranscript projects source records, not BuildContext's summaries.
// The display helpers also serve live output, but no lifecycle event is emitted.
func sessionTranscript(snapshot session.Snapshot) (*interaction.Transcript, error) {
	branch, err := session.ActiveBranch(snapshot)
	if err != nil {
		return nil, err
	}
	messages := make(map[string]session.MessageEntry, len(snapshot.Messages))
	for _, entry := range snapshot.Messages {
		messages[entry.ID] = entry
	}
	view := &interaction.Transcript{SessionID: snapshot.Header.ID}
	var desktopDisplay desktopDisplayProjection
	calls := make(map[string]llm.ToolCall)
	indices := make(map[string]int)
	for _, node := range branch {
		entry, ok := messages[node.ID]
		if !ok {
			continue
		}
		switch message := entry.Message.(type) {
		case llm.UserMessage:
			view.Entries = append(view.Entries, interaction.TranscriptEntry{
				ID: entry.ID, Kind: interaction.TranscriptUser, Text: userContent(message),
			})
		case llm.AssistantMessage:
			text, thinking := assistantContent(message)
			view.Entries = append(view.Entries, interaction.TranscriptEntry{
				ID: entry.ID, Kind: interaction.TranscriptAssistant,
				Assistant: interaction.AssistantDisplay{Text: text, Thinking: thinking, Concludes: assistantConcludes(message)},
			})
			for _, part := range message.Content {
				if part.Type != llm.ContentTypeToolCall || part.ToolCall == nil {
					continue
				}
				call := *part.ToolCall
				display := displayToolCall(call)
				display.Desktop = desktopDisplay.start(call)
				if display.Desktop != nil {
					display.Desktop.Phase = "Result not recorded"
					display.Failed = true
				}
				calls[call.ID] = call
				indices[call.ID] = len(view.Entries)
				view.Entries = append(view.Entries, interaction.TranscriptEntry{
					ID: entry.ID, Kind: interaction.TranscriptTool, Tool: display,
				})
			}
			if message.ErrorMessage != "" && (message.StopReason == llm.StopReasonError || message.StopReason == llm.StopReasonAborted) {
				view.Entries = append(view.Entries, interaction.TranscriptEntry{
					ID: entry.ID, Kind: interaction.TranscriptNotice, Text: message.ErrorMessage,
				})
			}
		case llm.ToolResultMessage:
			index, ok := indices[message.ToolCallID]
			if !ok {
				continue
			}
			call := calls[message.ToolCallID]
			event := agent.AgentEvent{ToolCall: &call, ToolResult: &message}
			tool := &view.Entries[index].Tool
			tool.Desktop = desktopDisplay.end(event)
			tool.Output = displayToolOutput(event)
			tool.Diff = displayToolDiff(event)
			tool.Truncation = displayToolTruncation(&message)
			tool.Evidence = displayToolEvidence(&message)
			tool.Failed = message.IsError
			view.Entries[index].ID = entry.ID
		}
	}
	return view, nil
}
