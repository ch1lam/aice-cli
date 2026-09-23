package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func testQuestionRequest() interaction.QuestionRequest {
	return interaction.QuestionRequest{
		ID: "call-1",
		Questions: []interaction.QuestionItem{
			{
				ID:       "mode",
				Header:   "执行方式",
				Question: "采用哪种行为？",
				Options: []interaction.QuestionOption{
					{ID: "check", Label: "仅检查"},
					{ID: "apply", Label: "直接修改"},
				},
			},
		},
	}
}

func answerFor(request interaction.QuestionRequest) interaction.QuestionReply {
	return interaction.QuestionReply{
		RequestID: request.ID,
		Answers: map[string]interaction.QuestionAnswer{
			"mode": {
				Status:           interaction.QuestionAnswered,
				SelectedOptionID: "check",
				SelectedLabel:    "仅检查",
			},
		},
	}
}

func TestAskQuestionDeliversSingleSubmission(t *testing.T) {
	t.Parallel()
	session := &interactiveSession{
		questionRequests: make(chan interaction.QuestionPrompt, questionBridgeLifetime),
	}
	request := testQuestionRequest()
	done := make(chan interaction.QuestionReply, 1)
	go func() {
		reply, err := session.AskQuestion(t.Context(), request)
		if err != nil {
			t.Errorf("AskQuestion() error = %v", err)
			return
		}
		done <- reply
	}()
	var prompt interaction.QuestionPrompt
	select {
	case prompt = <-session.QuestionRequests():
	case <-t.Context().Done():
		t.Fatal("no prompt published")
	}
	if prompt.Request.ID != "call-1" {
		t.Fatalf("prompt request = %#v", prompt.Request)
	}
	answered := answerFor(request)
	answered.RequestID = "call-1"
	select {
	case prompt.Reply <- answered:
	case <-t.Context().Done():
		t.Fatal("reply channel blocked")
	}
	select {
	case got := <-done:
		if !reflect.DeepEqual(got, answered) {
			t.Fatalf("reply = %#v, want %#v", got, answered)
		}
	case <-t.Context().Done():
		t.Fatal("no reply delivered")
	}
}

func TestAskQuestionRejectsMismatchedReply(t *testing.T) {
	t.Parallel()
	session := &interactiveSession{
		questionRequests: make(chan interaction.QuestionPrompt, questionBridgeLifetime),
	}
	request := testQuestionRequest()
	errResult := make(chan error, 1)
	go func() {
		_, err := session.AskQuestion(t.Context(), request)
		errResult <- err
	}()
	var prompt interaction.QuestionPrompt
	select {
	case prompt = <-session.QuestionRequests():
	case <-t.Context().Done():
		t.Fatal("no prompt published")
	}
	answered := answerFor(request)
	answered.RequestID = "call-2"
	prompt.Reply <- answered
	select {
	case err := <-errResult:
		if err == nil {
			t.Fatal("AskQuestion() = nil for mismatched reply, want error")
		}
	case <-t.Context().Done():
		t.Fatal("no result delivered")
	}
}

func TestAskQuestionHonorsCancellation(t *testing.T) {
	t.Parallel()
	session := &interactiveSession{
		questionRequests: make(chan interaction.QuestionPrompt, questionBridgeLifetime),
	}
	ctx, cancel := context.WithCancel(t.Context())
	errResult := make(chan error, 1)
	go func() {
		_, err := session.AskQuestion(ctx, testQuestionRequest())
		errResult <- err
	}()
	var prompt interaction.QuestionPrompt
	select {
	case prompt = <-session.QuestionRequests():
	case <-t.Context().Done():
		t.Fatal("no prompt published")
	}
	// A submission racing cancellation must not win.
	cancel()
	prompt.Reply <- answerFor(testQuestionRequest())
	select {
	case err := <-errResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AskQuestion() error = %v, want context.Canceled", err)
		}
	case <-t.Context().Done():
		t.Fatal("no result delivered")
	}
}

func TestAskQuestionWithoutFrontendFailsClosed(t *testing.T) {
	t.Parallel()
	session := &interactiveSession{}
	if _, err := session.AskQuestion(t.Context(), testQuestionRequest()); err == nil {
		t.Fatal("AskQuestion() = nil without frontend, want error")
	}
}

func TestBuiltInToolsGateQuestionByMode(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t, t.TempDir())
	names := func(interactive bool) []string {
		tools, err := newBuiltInTools(t.Context(), workspace, interactive)
		if err != nil {
			t.Fatalf("newBuiltInTools() error = %v", err)
		}
		got := make([]string, 0, len(tools))
		for _, current := range tools {
			got = append(got, current.Definition().Name)
		}
		return got
	}
	nonInteractive := names(false)
	for _, name := range nonInteractive {
		if name == "request_user_input" {
			t.Fatalf("non-interactive tools expose request_user_input: %v", nonInteractive)
		}
	}
	interactive := names(true)
	found := false
	for _, name := range interactive {
		if name == "request_user_input" {
			found = true
		}
	}
	if !found {
		t.Fatalf("interactive tools omit request_user_input: %v", interactive)
	}
}
