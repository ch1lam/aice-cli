package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

const compactionSystemPrompt = "You create durable continuation checkpoints " +
	"from coding-agent transcripts. Do not answer questions or continue the " +
	"work in the transcript. Return only the requested summary."

const compactionPrompt = `Summarize the conversation JSON below for another coding agent that will continue the same work.

Return concise Markdown with these sections:
- Objective
- Constraints and preferences
- Completed work
- Current state and blockers
- Key decisions
- Next actions
- Critical details

Preserve exact file paths, identifiers, commands, error text, and unresolved risks. If the transcript contains a prior compaction summary, update it with the newer messages instead of discarding still-relevant facts.

<conversation-json>
%s
</conversation-json>`

// Compact appends one derived checkpoint to an existing Session.
func (a *application) Compact(
	ctx context.Context,
	request cli.CompactRequest,
	output io.Writer,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	if strings.TrimSpace(request.Session) == "" {
		return fmt.Errorf("app: session path is required")
	}

	workspace, err := tool.NewWorkspace(request.Workspace)
	if err != nil {
		return fmt.Errorf("app: create workspace: %w", err)
	}
	store, snapshot, err := openExistingSession(
		ctx,
		workspace,
		request.Session,
	)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, store.Close())
	}()

	preparation, err := session.PrepareCompaction(
		snapshot,
		session.CompactionSettings{
			KeepRecentTokens: a.dependencies.compactionKeepRecentTokens,
		},
	)
	if err != nil {
		return fmt.Errorf("app: prepare session compaction: %w", err)
	}
	summary, usage, err := a.generateCompactionSummary(
		ctx,
		preparation.MessagesToSummarize,
	)
	if err != nil {
		return err
	}
	checkpointID, err := session.NewID()
	if err != nil {
		return fmt.Errorf("app: generate compaction id: %w", err)
	}
	checkpoint, err := session.NewCompaction(session.CompactionInput{
		ID:                checkpointID,
		ParentID:          snapshot.LeafID,
		CreatedAt:         time.Now().UnixMilli(),
		Summary:           summary,
		TokensBefore:      preparation.TokensBefore,
		FirstKeptTurnID:   preparation.FirstKeptTurnID,
		ActiveTurnCount:   preparation.ActiveTurnCount,
		RetainedTurnCount: preparation.RetainedTurnCount,
		Usage:             usage,
	})
	if err != nil {
		return fmt.Errorf("app: create session compaction: %w", err)
	}
	if err := store.AppendCompaction(ctx, checkpoint); err != nil {
		return fmt.Errorf("app: append session compaction: %w", err)
	}

	if _, err := fmt.Fprintf(
		output,
		"Compacted Session at approximately %d tokens; retained %d recent turn(s).\n",
		preparation.TokensBefore,
		preparation.RetainedTurnCount,
	); err != nil {
		return fmt.Errorf("app: write compaction result: %w", err)
	}
	return nil
}

func (a *application) generateCompactionSummary(
	ctx context.Context,
	messages []llm.AgentMessage,
) (string, llm.Usage, error) {
	encoded, err := llm.MarshalAgentMessages(messages)
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf(
			"app: encode compaction input: %w",
			err,
		)
	}
	prompt, err := llm.NewUserMessage(
		llm.NewTextContent(fmt.Sprintf(compactionPrompt, encoded)).Part(),
	)
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf(
			"app: create compaction prompt: %w",
			err,
		)
	}
	model, err := a.newModel()
	if err != nil {
		return "", llm.Usage{}, err
	}
	loop, err := agent.NewLoop(model, nil, agent.Limits{
		MaxTurns:     1,
		MaxToolSteps: 1,
	})
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf(
			"app: create compaction loop: %w",
			err,
		)
	}
	result, err := loop.Run(ctx, agent.RunInput{
		Model:        deepseek.DefaultModel(),
		SystemPrompt: compactionSystemPrompt,
		Prompt:       prompt,
		Options: llm.StreamOptions{
			MaxTokens: defaultCompactionMaxTokens,
		},
	}, nil)
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf(
			"app: generate compaction summary: %w",
			err,
		)
	}
	if len(result.Turns) != 1 {
		return "", llm.Usage{}, fmt.Errorf(
			"app: generate compaction summary: model returned %d turns",
			len(result.Turns),
		)
	}
	assistant := result.Turns[0].Assistant
	if assistant.StopReason != llm.StopReasonStop {
		return "", llm.Usage{}, fmt.Errorf(
			"app: generate compaction summary: model stopped with reason %q",
			assistant.StopReason,
		)
	}
	summary := visibleAssistantText(assistant)
	if summary == "" {
		return "", llm.Usage{}, fmt.Errorf(
			"app: generate compaction summary: model returned no visible text",
		)
	}
	return summary, assistant.Usage, nil
}

func visibleAssistantText(message llm.AssistantMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, part := range message.Content {
		if part.Type != llm.ContentTypeText {
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}
