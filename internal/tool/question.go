package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// RequestUserInput asks the user 1-3 focused questions through the active
// frontend and returns the submitted group as one structured result. Option
// drafts, focus, and per-question edits never produce intermediate results:
// only an explicit submit (or explicit per-question skip) answers the call.
type RequestUserInput struct {
	asker interaction.QuestionAsker
}

// NewRequestUserInput constructs the question tool. A nil asker means no
// frontend can answer (non-interactive runs, side threads); Execute then
// fails closed so the model states the missing information instead of
// waiting on terminal input that will never arrive.
func NewRequestUserInput(asker interaction.QuestionAsker) *RequestUserInput {
	return &RequestUserInput{asker: asker}
}

// SetAsker binds the active frontend. It is called once the interactive
// Session exists; tools built for the run environment start unbound.
func (q *RequestUserInput) SetAsker(asker interaction.QuestionAsker) {
	if q == nil {
		return
	}
	q.asker = asker
}

// Definition returns the model-facing question contract.
func (q *RequestUserInput) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "request_user_input",
		Description: "Ask the user 1-3 focused questions when a choice materially affects " +
			"scope, outcome, or rework cost. Use it to resolve requirements, delivery " +
			"shape, compatibility, or preferences you cannot determine from the " +
			"repository, docs, or conversation. Do not use it for permissions, " +
			"credentials, or anything you can decide or discover yourself.",
		InputSchema: jsonSchema(`{
			"type": "object",
			"properties": {
				"questions": {
					"type": "array",
					"minItems": 1,
					"maxItems": 3,
					"items": {
						"type": "object",
						"properties": {
							"id": {"type": "string"},
							"header": {"type": "string"},
							"question": {"type": "string"},
							"options": {
								"type": "array",
								"maxItems": 6,
								"items": {
									"type": "object",
									"properties": {
										"id": {"type": "string"},
										"label": {"type": "string"},
										"description": {"type": "string"}
									},
									"required": ["id", "label"],
									"additionalProperties": false
								}
							},
							"recommended_option_id": {"type": "string"}
						},
						"required": ["id", "question"],
						"additionalProperties": false
					}
				}
			},
			"required": ["questions"],
			"additionalProperties": false
		}`),
		PromptSnippet: "Ask the user 1-3 focused questions when a choice materially affects the outcome",
		PromptGuidelines: []string{
			"Check the repository, docs, and conversation first; never ask for information you can obtain yourself",
			"Decide routine implementation choices yourself; ask only when a choice materially affects scope, outcome, or rework cost",
			"Keep asking efficient: at most 3 questions per call, one idea per question, and never re-ask settled requirements",
			"Explain the practical difference of each option; mark at most one recommended option per question without assuming it is chosen",
			"Ask dependent questions in separate rounds once their premise is known; accept skipped questions and continue with what stands alone",
		},
	}
}

type questionArguments struct {
	Questions []interaction.QuestionItem `json:"questions"`
}

// Execute validates the call, waits for one explicit submission, and returns
// the structured answers. Invalid parameters never open the panel.
func (q *RequestUserInput) Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
	args, err := decodeArguments[questionArguments](ctx, call, "request_user_input")
	if err != nil {
		return llm.ToolResult{}, err
	}
	request := interaction.QuestionRequest{ID: call.ID, Questions: args.Questions}
	if err := interaction.ValidateQuestionRequest(request); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "request_user_input", err)
	}
	if q == nil || q.asker == nil {
		return llm.ToolResult{}, fmt.Errorf(
			"tool %q is not available in this run: no frontend can answer questions; "+
				"state the missing information and the affected scope in your output instead of waiting",
			"request_user_input",
		)
	}
	reply, err := q.asker.AskQuestion(ctx, request)
	if err != nil {
		return llm.ToolResult{}, err
	}
	// A submission racing cancellation must not become a valid answer.
	if err := ctx.Err(); err != nil {
		return llm.ToolResult{}, err
	}
	if err := interaction.ValidateQuestionReply(request, reply); err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool %q: %w", "request_user_input", err)
	}
	payload, err := json.Marshal(struct {
		Answers map[string]interaction.QuestionAnswer `json:"answers"`
	}{Answers: reply.Answers})
	if err != nil {
		return llm.ToolResult{}, fmt.Errorf("tool %q: encode answers: %w", "request_user_input", err)
	}
	return textResult(call, string(payload), false), nil
}

// QuestionSummary renders one compact history line per answered question for
// display projections. It never parses model prose.
func QuestionSummary(request interaction.QuestionRequest, reply interaction.QuestionReply) string {
	rows := make([]string, 0, len(request.Questions))
	for _, item := range request.Questions {
		answer, ok := reply.Answers[item.ID]
		if !ok || answer.Status == interaction.QuestionSkipped {
			continue
		}
		title := item.Header
		if strings.TrimSpace(title) == "" {
			title = item.Question
		}
		detail := answer.SelectedLabel
		if strings.TrimSpace(detail) == "" {
			detail = answer.Text
		} else if strings.TrimSpace(answer.Text) != "" {
			detail += " (" + strings.TrimSpace(answer.Text) + ")"
		}
		rows = append(rows, strings.TrimSpace(title)+"："+strings.TrimSpace(detail))
	}
	return strings.Join(rows, "\n")
}
