package agent_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestReasoningRejectionArmsDegradedRetry(t *testing.T) {
	t.Parallel()
	info := testModel()
	rejected := errors.New(
		"openai responses: start response stream: " +
			"400 invalid_request_error: reasoning `encrypted_content` was not issued to this caller",
	)
	model := &scriptedModel{scripts: []*streamScript{
		{nextErr: rejected},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "hi")), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want the rejection plus one self-heal retry", len(model.requests))
	}
	if model.requests[0].Options.FilterReasoningHistory {
		t.Fatal("initial request already filters reasoning history")
	}
	if !model.requests[1].Options.FilterReasoningHistory {
		t.Fatal("self-heal retry does not filter reasoning history")
	}
	rounds := result.ModelRounds
	if len(rounds) != 2 {
		t.Fatalf("model rounds = %d, want failed attempt plus answer", len(rounds))
	}
	if !strings.Contains(rounds[0].Assistant.ErrorMessage, "model request failed before completion") {
		t.Fatalf("failed attempt not recorded: %#v", rounds[0].Assistant)
	}
	if rounds[1].Assistant.StopReason != llm.StopReasonStop {
		t.Fatalf("answer stop reason = %v, want stop", rounds[1].Assistant.StopReason)
	}
}

func TestReasoningRejectionFailsAfterDegradedRetry(t *testing.T) {
	t.Parallel()
	info := testModel()
	rejected := errors.New(
		"openai responses: start response stream: " +
			"400 invalid_request_error: reasoning `encrypted_content` was not issued to this caller",
	)
	model := &scriptedModel{scripts: []*streamScript{
		{nextErr: rejected},
		{nextErr: rejected},
	}}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "hi")), nil); err == nil {
		t.Fatal("run succeeded although the degraded retry also failed")
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want exactly two attempts and no retry loop", len(model.requests))
	}
}
