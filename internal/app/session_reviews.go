package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// SessionReviews reconstructs what the classifier saw for each recorded auto
// mode decision on the active branch. Each line is a classify.Case whose
// expect is the classifier's decision and whose set is empty: a person labels
// it before it joins internal/classify/eval.
func (a *application) SessionReviews(ctx context.Context, request cli.SessionTreeRequest, output io.Writer) (returnErr error) {
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	workspace, err := tool.NewWorkspace(request.Workspace)
	if err != nil {
		return fmt.Errorf("app: create workspace: %w", err)
	}
	store, snapshot, err := openExistingSession(ctx, workspace, request.Session)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	entries, err := activeEntries(store, nil)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	for _, item := range reviewCases(snapshot.Header.ID, entries) {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func reviewCases(sessionID string, entries []session.MessageEntry) []classify.Case {
	calls := map[string]llm.ToolCall{}
	var cases []classify.Case
	for i, entry := range entries {
		switch message := entry.Message.(type) {
		case llm.AssistantMessage:
			for _, part := range message.Content {
				if part.Type == llm.ContentTypeToolCall && part.ToolCall != nil {
					calls[part.ToolCall.ID] = *part.ToolCall
				}
			}
		case llm.ToolResultMessage:
			call, ok := calls[message.ToolCallID]
			if !ok || message.Details == nil || message.Details.Review == nil {
				continue
			}
			review := message.Details.Review
			req := transcriptRequest(entries[:i], call)
			item := classify.Case{
				ID:      sessionID + "-" + call.ID,
				Intent:  []string{},
				History: req.History,
				Action:  classify.Action{Tool: call.Name, Arguments: call.Arguments},
				Expect:  classify.Decision(review.Decision),
				Note:    fmt.Sprintf("%s by %s (stage %d): %s", review.Decision, review.Classifier, review.Stage, review.Reason),
			}
			for _, turn := range req.Intent {
				item.Intent = append(item.Intent, turn.Text)
			}
			cases = append(cases, item)
		}
	}
	return cases
}
