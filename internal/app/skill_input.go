package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

const maximumInputSkills = 8

func (s *interactiveSession) prepareAttachedInput(ctx context.Context, input interaction.RunInput) (interaction.RunInput, error) {
	input, err := s.prepareSkillInput(ctx, input)
	if err != nil {
		return interaction.RunInput{}, err
	}
	return prepareFileInput(ctx, input, s.workspace, s.guardAdapter, s.handleGuardAsk)
}

// Resolve explicit references from the immutable startup catalog before input
// acceptance. No marker parsing or lazy disk reads occur in the Loop/mailbox.
// The accepted user message (including instructions) is the durable snapshot.
func (s *interactiveSession) prepareSkillInput(ctx context.Context, input interaction.RunInput) (interaction.RunInput, error) {
	if err := ctx.Err(); err != nil {
		return interaction.RunInput{}, err
	}
	if len(input.Skills) == 0 {
		return input, nil
	}
	if len(input.Skills) > maximumInputSkills {
		return interaction.RunInput{}, fmt.Errorf("at most %d skills can be attached per input", maximumInputSkills)
	}
	var entries []tool.SkillEntry
	seen := make(map[string]bool)
	for _, name := range input.Skills {
		item, ok := s.skills.Lookup(name)
		if !ok {
			return interaction.RunInput{}, fmt.Errorf("unknown attached skill %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, tool.SkillEntry{Name: item.Name, Dir: item.Dir, Body: item.Body})
	}
	loader := tool.NewSkill(entries)
	var text strings.Builder
	text.WriteString(input.Prompt)
	text.WriteString("\n\nUse the following explicitly attached skills for this task. Their full instructions are already loaded.\n")
	for _, entry := range entries {
		args, _ := json.Marshal(map[string]string{"name": entry.Name})
		call := llm.ToolCall{ID: "attachment", Name: "skill", Arguments: args}
		if err := authorizeInputTool(ctx, call, entry.Name, s.guardAdapter, s.handleGuardAsk); err != nil {
			return interaction.RunInput{}, err
		}
		result, err := loader.Execute(ctx, call)
		if err != nil {
			return interaction.RunInput{}, err
		}
		if result.IsError {
			return interaction.RunInput{}, fmt.Errorf("could not attach skill %q", entry.Name)
		}
		for _, part := range result.Content {
			if part.Type == llm.ContentTypeText {
				text.WriteString("\n" + part.Text + "\n")
			}
		}
	}
	input.Prompt, input.Skills = text.String(), nil
	return input, nil
}
