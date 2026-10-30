package acp

import (
	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// replay renders a loaded branch as the updates a client would have seen.
// Notices are AICE presentation and have no ACP counterpart.
func replay(transcript interaction.Transcript) []sdk.SessionUpdate {
	var updates []sdk.SessionUpdate
	for _, entry := range transcript.Entries {
		switch entry.Kind {
		case interaction.TranscriptUser:
			updates = append(updates, sdk.UpdateUserMessageText(entry.Text))
		case interaction.TranscriptAssistant:
			if entry.Assistant.Thinking != "" {
				updates = append(updates, sdk.UpdateAgentThoughtText(entry.Assistant.Thinking))
			}
			if entry.Assistant.Text != "" {
				updates = append(updates, sdk.UpdateAgentMessageText(entry.Assistant.Text))
			}
		case interaction.TranscriptTool:
			updates = append(updates, toolStart(entry.Tool))
			if entry.Tool.Output.Available || entry.Tool.Failed {
				updates = append(updates, toolEnd(entry.Tool))
			}
		}
	}
	return updates
}
