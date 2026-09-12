package app

import (
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const maximumDisplayToolOutputBytes = 64 * 1024

// Project the recorded result, never reread a file that a tool may have changed.
// Stop copying at the display budget without altering the original result.
func displayToolOutput(event agent.AgentEvent) interaction.ToolOutputDisplay {
	output := interaction.ToolOutputDisplay{Available: event.ToolResult != nil || event.Err != nil}
	var text strings.Builder
	appendPart := func(part string) {
		remaining := maximumDisplayToolOutputBytes - text.Len()
		if len(part) > remaining {
			output.Truncated = true
			for remaining > 0 && !utf8.RuneStart(part[remaining]) {
				remaining--
			}
			part = part[:remaining]
		}
		text.WriteString(part)
	}
	if event.ToolResult != nil {
		for index, part := range event.ToolResult.Content {
			if index > 0 {
				appendPart("\n")
			}
			switch part.Type {
			case llm.ContentTypeText:
				appendPart(part.Text)
			case llm.ContentTypeImage:
				appendPart("[Image result]")
			default:
				appendPart("[Non-text result]")
			}
			if output.Truncated {
				break
			}
		}
	}
	if text.Len() == 0 && event.Err != nil {
		appendPart(event.Err.Error())
	}
	output.Text = text.String()
	return output
}
