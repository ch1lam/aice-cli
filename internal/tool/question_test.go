package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type stubAsker struct {
	reply interaction.QuestionReply
	err   error
	got   interaction.QuestionRequest
}

func (s *stubAsker) AskQuestion(
	ctx context.Context,
	request interaction.QuestionRequest,
) (interaction.QuestionReply, error) {
	s.got = request
	if err := ctx.Err(); err != nil {
		return interaction.QuestionReply{}, err
	}
	if s.err != nil {
		return interaction.QuestionReply{}, s.err
	}
	return s.reply, nil
}

func questionCall(t *testing.T, arguments string) llm.ToolCall {
	t.Helper()
	return llm.ToolCall{ID: "call-1", Name: "request_user_input", Arguments: json.RawMessage(arguments)}
}

const validQuestionArguments = `{"questions": [{
	"id": "execution_mode",
	"header": "执行方式",
	"question": "直接运行修复命令时，希望采用哪种行为？",
	"options": [
		{"id": "check", "label": "仅检查并显示建议", "description": "先审阅，再决定是否修改"},
		{"id": "apply", "label": "直接修改文件", "description": "适合自动修复场景"}
	],
	"recommended_option_id": "check"
}]}`

func TestRequestUserInputDefinition(t *testing.T) {
	t.Parallel()
	definition := tool.NewRequestUserInput(nil).Definition()
	if definition.Name != "request_user_input" {
		t.Fatalf("definition name = %q", definition.Name)
	}
	if !json.Valid(definition.InputSchema) {
		t.Fatalf("schema is invalid json: %s", definition.InputSchema)
	}
	if err := definition.Validate(); err != nil {
		t.Fatalf("definition.Validate() error = %v", err)
	}
}

func TestRequestUserInputRejectsInvalidArgumentsWithoutAsking(t *testing.T) {
	t.Parallel()
	asker := &stubAsker{}
	current := tool.NewRequestUserInput(asker)
	for _, arguments := range []string{
		`{}`,
		`{"questions": []}`,
		`{"questions": [
			{"id": "a", "question": "q1"},
			{"id": "b", "question": "q2"},
			{"id": "c", "question": "q3"},
			{"id": "d", "question": "q4"}
		]}`,
		`{"questions": [{"id": "a", "question": "q", "recommended_option_id": "ghost"}]}`,
	} {
		if _, err := current.Execute(t.Context(), questionCall(t, arguments)); err == nil {
			t.Fatalf("Execute(%s) = nil, want error", arguments)
		}
	}
	if asker.got.ID != "" {
		t.Fatalf("asker was consulted for invalid arguments: %#v", asker.got)
	}
}

func TestRequestUserInputWithoutAskerFailsClosed(t *testing.T) {
	t.Parallel()
	current := tool.NewRequestUserInput(nil)
	_, err := current.Execute(t.Context(), questionCall(t, validQuestionArguments))
	if err == nil {
		t.Fatal("Execute() = nil without asker, want error")
	}
}

func TestRequestUserInputReturnsStructuredAnswers(t *testing.T) {
	t.Parallel()
	asker := &stubAsker{reply: interaction.QuestionReply{
		RequestID: "call-1",
		Answers: map[string]interaction.QuestionAnswer{
			"execution_mode": {
				Status:           interaction.QuestionAnswered,
				SelectedOptionID: "check",
				SelectedLabel:    "仅检查并显示建议",
				Text:             "只允许修改当前目录",
			},
		},
	}}
	result, err := tool.NewRequestUserInput(asker).Execute(
		t.Context(),
		questionCall(t, validQuestionArguments),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("result is error: %#v", result)
	}
	if asker.got.ID != "call-1" || len(asker.got.Questions) != 1 {
		t.Fatalf("asker got %#v", asker.got)
	}
	text := resultText(t, result)
	var payload struct {
		Answers map[string]interaction.QuestionAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("result is not answers JSON %q: %v", text, err)
	}
	answer := payload.Answers["execution_mode"]
	if answer.Status != interaction.QuestionAnswered ||
		answer.SelectedOptionID != "check" ||
		answer.Text != "只允许修改当前目录" {
		t.Fatalf("answers = %#v", payload.Answers)
	}
}

func TestRequestUserInputPropagatesCancellation(t *testing.T) {
	t.Parallel()
	asker := &stubAsker{err: context.Canceled}
	_, err := tool.NewRequestUserInput(asker).Execute(
		t.Context(),
		questionCall(t, validQuestionArguments),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
}

func TestRequestUserInputDiscardsSubmitRacingCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	asker := &stubAsker{reply: interaction.QuestionReply{
		RequestID: "call-1",
		Answers: map[string]interaction.QuestionAnswer{
			"execution_mode": {Status: interaction.QuestionSkipped},
		},
	}}
	// The bridge normally wins cancellation before returning a reply; the
	// tool keeps the same invariant for any asker that does not.
	cancel()
	_, err := tool.NewRequestUserInput(asker).Execute(ctx, questionCall(t, validQuestionArguments))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
}

func TestQuestionSummary(t *testing.T) {
	t.Parallel()
	request := interaction.QuestionRequest{Questions: []interaction.QuestionItem{
		{ID: "a", Header: "执行方式", Question: "行为？"},
		{ID: "b", Question: "目标？"},
	}}
	reply := interaction.QuestionReply{Answers: map[string]interaction.QuestionAnswer{
		"a": {
			Status:           interaction.QuestionAnswered,
			SelectedOptionID: "check",
			SelectedLabel:    "仅检查",
			Text:             "不递归",
		},
		"b": {Status: interaction.QuestionSkipped},
	}}
	summary := tool.QuestionSummary(request, reply)
	if !strings.Contains(summary, "执行方式") || !strings.Contains(summary, "仅检查") {
		t.Fatalf("summary = %q", summary)
	}
	if strings.Contains(summary, "目标") {
		t.Fatalf("summary must omit skipped questions: %q", summary)
	}
}
