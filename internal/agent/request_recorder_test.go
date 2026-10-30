package agent_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestRequestRecorderPrecedesEveryCallAndNamesItsMessage(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := repeatedToolModel(info, []string{`{"a":1}`})
	loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("read", nil)}, agent.WithGuard(allowAllGuard{}))
	if err != nil {
		t.Fatal(err)
	}
	var recorded []llm.RequestConfig
	input := testInput(info, mustPrompt(t, "inspect"))
	input.RequestRecorder = func(_ context.Context, config llm.RequestConfig) (string, error) {
		if len(model.requests) != len(recorded) {
			return "", errors.New("configuration recorded after its call")
		}
		recorded = append(recorded, config)
		return fmt.Sprintf("config-%d", len(recorded)), nil
	}
	result, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 2 || len(result.ModelRounds) != 2 {
		t.Fatalf("recorded %d configurations for %d rounds", len(recorded), len(result.ModelRounds))
	}
	for i, round := range result.ModelRounds {
		if want := fmt.Sprintf("config-%d", i+1); round.Assistant.RequestConfig != want {
			t.Fatalf("round %d config = %q, want %q", i, round.Assistant.RequestConfig, want)
		}
		if recorded[i].SystemPrompt != model.requests[i].SystemPrompt || len(recorded[i].Tools) != len(model.requests[i].Tools) {
			t.Fatalf("round %d recorded %+v for request %+v", i, recorded[i], model.requests[i])
		}
	}
}

func TestRequestRecorderFailureStopsBeforeTheCall(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := repeatedToolModel(info, nil)
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("disk full")
	input := testInput(info, mustPrompt(t, "inspect"))
	input.RequestRecorder = func(context.Context, llm.RequestConfig) (string, error) { return "", failure }
	if _, err := loop.Run(t.Context(), input, nil); !errors.Is(err, failure) {
		t.Fatalf("Run() error = %v, want recorder failure", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("model called %d times after recording failed", len(model.requests))
	}
}
