package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type answerAsker struct {
	reply interaction.QuestionReply
}

func (a answerAsker) AskQuestion(
	_ context.Context,
	request interaction.QuestionRequest,
) (interaction.QuestionReply, error) {
	reply := a.reply
	reply.RequestID = request.ID
	return reply, nil
}

// TestLoopQuestionAnswersReachNextModelRequest is the core Q&A acceptance:
// after the user submits, the next model round accurately receives the
// selected option and the free-text note.
func TestLoopQuestionAnswersReachNextModelRequest(t *testing.T) {
	t.Parallel()
	modelInfo := testModel()
	asker := answerAsker{reply: interaction.QuestionReply{
		Answers: map[string]interaction.QuestionAnswer{
			"mode": {
				Status:           interaction.QuestionAnswered,
				SelectedOptionID: "check",
				SelectedLabel:    "仅检查",
				Text:             "只改当前目录",
			},
		},
	}}
	question := tool.NewRequestUserInput(asker)
	first := assistantMessage(modelInfo, llm.StopReasonToolUse,
		toolCallPart("q-1", "request_user_input", `{"questions": [{
			"id": "mode", "header": "执行方式", "question": "采用哪种行为？",
			"options": [
				{"id": "check", "label": "仅检查"},
				{"id": "apply", "label": "直接修改"}
			]
		}]}`))
	last := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(last)},
	}}
	loop := mustLoop(t, model, []agent.Tool{question})
	input := testInput(modelInfo, mustPrompt(t, "fix it"))
	var recorded []llm.ToolResultMessage
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		if result, ok := message.(llm.ToolResultMessage); ok {
			recorded = append(recorded, result)
		}
		return nil
	}
	result, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 2 || len(recorded) != 1 {
		t.Fatalf("requests=%d recorded=%d, want 2 and 1", len(model.requests), len(recorded))
	}
	answered := recorded[0]
	if answered.IsError || answered.ToolCallID != "q-1" || answered.ToolName != "request_user_input" {
		t.Fatalf("tool result = %#v", answered)
	}
	text := messageText(answered)
	var payload struct {
		Answers map[string]interaction.QuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("result is not answers JSON %q: %v", text, err)
	}
	answer := payload.Answers["mode"]
	if answer.SelectedOptionID != "check" || answer.Text != "只改当前目录" {
		t.Fatalf("answers = %#v", payload.Answers)
	}
	// The recorded result must be the exact message the next model request
	// carries; steering text must not leak into the structured answers.
	next := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	got, ok := next.(llm.ToolResultMessage)
	if !ok || got.ToolCallID != "q-1" || !strings.Contains(messageText(got), "只改当前目录") {
		t.Fatalf("next request tail = %#v", next)
	}
	if len(result.ModelRounds) != 2 {
		t.Fatalf("rounds = %d, want 2", len(result.ModelRounds))
	}
}

// TestLoopQuestionCustomTextReachesNextModelRequest covers the custom-answer
// branch of the same path: an option question answered with free text (empty
// selection, non-blank text) must validate, return structured answers, and
// reach the next model request intact.
func TestLoopQuestionCustomTextReachesNextModelRequest(t *testing.T) {
	t.Parallel()
	modelInfo := testModel()
	asker := answerAsker{reply: interaction.QuestionReply{
		Answers: map[string]interaction.QuestionAnswer{
			"mode": {
				Status: interaction.QuestionAnswered,
				Text:   "先做最小实现，不增加新依赖",
			},
		},
	}}
	question := tool.NewRequestUserInput(asker)
	first := assistantMessage(modelInfo, llm.StopReasonToolUse,
		toolCallPart("q-1", "request_user_input", `{"questions": [{
			"id": "mode", "header": "执行方式", "question": "采用哪种行为？",
			"options": [
				{"id": "check", "label": "仅检查"},
				{"id": "apply", "label": "直接修改"}
			]
		}]}`))
	last := assistantMessage(modelInfo, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)},
		{events: terminalEvents(last)},
	}}
	loop := mustLoop(t, model, []agent.Tool{question})
	input := testInput(modelInfo, mustPrompt(t, "fix it"))
	var recorded []llm.ToolResultMessage
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		if result, ok := message.(llm.ToolResultMessage); ok {
			recorded = append(recorded, result)
		}
		return nil
	}
	result, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 2 || len(recorded) != 1 {
		t.Fatalf("requests=%d recorded=%d, want 2 and 1", len(model.requests), len(recorded))
	}
	text := messageText(recorded[0])
	var payload struct {
		Answers map[string]interaction.QuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("result is not answers JSON %q: %v", text, err)
	}
	answer := payload.Answers["mode"]
	if answer.Status != interaction.QuestionAnswered ||
		answer.SelectedOptionID != "" ||
		answer.SelectedLabel != "" ||
		answer.Text != "先做最小实现，不增加新依赖" {
		t.Fatalf("answers = %#v", payload.Answers)
	}
	next := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	got, ok := next.(llm.ToolResultMessage)
	if !ok || got.ToolCallID != "q-1" || !strings.Contains(messageText(got), "先做最小实现") {
		t.Fatalf("next request tail = %#v", next)
	}
	if len(result.ModelRounds) != 2 {
		t.Fatalf("rounds = %d, want 2", len(result.ModelRounds))
	}
}
