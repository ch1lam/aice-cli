package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

const maximumInputFiles = 8

// prepareFileInput freezes authorized file contents before the input is accepted.
// It uses the same bounded reader as model tool calls; no paths reach the mailbox.
func prepareFileInput(ctx context.Context, input interaction.RunInput, workspace *tool.Workspace, gate *guardAdapter,
	ask func(context.Context, llm.ToolCall, agent.GuardApproval) (agent.GuardAskReply, error),
) (interaction.RunInput, error) {
	if err := ctx.Err(); err != nil {
		return interaction.RunInput{}, err
	}
	if len(input.Files) == 0 {
		return input, nil
	}
	if len(input.Files) > maximumInputFiles {
		return interaction.RunInput{}, fmt.Errorf("at most %d files can be attached per input", maximumInputFiles)
	}
	reader, err := tool.NewRead(workspace)
	if err != nil {
		return interaction.RunInput{}, err
	}
	input.Images = interaction.CloneImages(input.Images)
	var text strings.Builder
	text.WriteString(input.Prompt)
	seen := make(map[string]bool)
	for _, path := range input.Files {
		args, _ := json.Marshal(tool.ReadRequest{Path: path})
		call := llm.ToolCall{ID: "attachment", Name: "read", Arguments: args}
		decision, err := gate.Check(ctx, call)
		if err != nil {
			return interaction.RunInput{}, err
		}
		if decision.Decision == agent.GuardDeny {
			return interaction.RunInput{}, fmt.Errorf("attach %q: %s", path, decision.Reason)
		}
		for _, approval := range decision.Approvals {
			if ask == nil {
				return interaction.RunInput{}, fmt.Errorf("attach %q: %s (approval requires interactive mode)", path, approval.Reason)
			}
			reply, err := ask(ctx, call, approval)
			if err != nil {
				return interaction.RunInput{}, err
			}
			if reply.Decision != agent.GuardAllow {
				return interaction.RunInput{}, fmt.Errorf("attach %q: permission denied", path)
			}
		}
		resolved, err := reader.ResolvePath(path)
		if err != nil {
			return interaction.RunInput{}, err
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		// Use the same input spelling checked by the gate. A physical symlink
		// target may contain Unicode spaces that must not be folded a second time.
		content, err := reader.Content(ctx, tool.ReadRequest{Path: path})
		if err != nil {
			return interaction.RunInput{}, fmt.Errorf("attach %q: %w", path, err)
		}
		for _, part := range content {
			if part.Image != nil {
				input.Images = append(input.Images, part.Image.Clone())
			}
			if part.Type == llm.ContentTypeText {
				fmt.Fprintf(&text, "\n\n[File %q]\n%s\n[End file]\n", resolved, part.Text)
			}
		}
		if err := interaction.ValidateImages(input.Images); err != nil {
			return interaction.RunInput{}, err
		}
	}
	input.Prompt = text.String()
	input.Files = nil
	return input, nil
}
