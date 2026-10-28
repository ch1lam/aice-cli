package classify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// fakeModel answers each stage from a script and records the requests.
type fakeModel struct {
	mu       sync.Mutex
	fast     string
	fastErr  error
	reason   string
	err      error
	delay    time.Duration
	requests []Completion
}

func (m *fakeModel) Complete(ctx context.Context, req Completion) (string, llm.Usage, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	stage := len(m.requests)
	m.mu.Unlock()
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", llm.Usage{}, ctx.Err()
		}
	}
	usage := llm.Usage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105}
	if stage == 1 {
		return m.fast, usage, m.fastErr
	}
	return m.reason, usage, m.err
}

func newTestClassifier(t *testing.T, model Model) *Classifier {
	t.Helper()
	c, err := New(Config{Model: model, ModelName: "fake", FastTimeout: time.Second, ReasonTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testRequest() Request {
	return Request{
		Intent:      []UserTurn{{Text: "push my branch"}},
		Action:      Action{Tool: "bash", Arguments: json.RawMessage(`{"command":"git push origin feature/login"}`)},
		Environment: Environment{Workspace: "/w", GitBranch: "feature/login"},
	}
}

func TestClassifyStages(t *testing.T) {
	tests := []struct {
		name      string
		model     *fakeModel
		decision  Decision
		stage     int
		requests  int
		reasonHas string
	}{
		{"fast allow", &fakeModel{fast: "ALLOW"}, Allow, 1, 1, ""},
		{"fast allow with punctuation", &fakeModel{fast: " allow.\n"}, Allow, 1, 1, ""},
		{"fast block then reasoned allow", &fakeModel{fast: "BLOCK", reason: "steps...\n" + `{"decision":"allow","rule":"","reason":"user asked to push"}`}, Allow, 2, 2, "user asked"},
		{"reasoned block", &fakeModel{fast: "BLOCK", reason: `{"decision":"block","rule":"others.shared","reason":"main is shared"}`}, Block, 2, 2, "main is shared"},
		{"fast explanation goes to stage two", &fakeModel{fast: "ALLOW because it is fine", reason: `{"decision":"block","rule":"x","reason":"r"}`}, Block, 2, 2, ""},
		{"fast error goes to stage two", &fakeModel{fastErr: errors.New("boom"), reason: `{"decision":"allow","rule":"","reason":""}`}, Allow, 2, 2, ""},
		{"invalid output blocks", &fakeModel{fast: "BLOCK", reason: "I think it is fine"}, Block, 2, 2, "invalid"},
		{"unknown decision blocks", &fakeModel{fast: "BLOCK", reason: `{"decision":"maybe","rule":"","reason":""}`}, Block, 2, 2, "invalid"},
		{"extra field blocks", &fakeModel{fast: "BLOCK", reason: `{"decision":"allow","rule":"","reason":"","override":true}`}, Block, 2, 2, "invalid"},
		{"model error is unavailable", &fakeModel{fast: "BLOCK", err: errors.New("provider down")}, Unavailable, 2, 2, "provider down"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v, err := newTestClassifier(t, test.model).Classify(t.Context(), testRequest())
			if err != nil {
				t.Fatal(err)
			}
			if v.Decision != test.decision || v.Stage != test.stage || len(test.model.requests) != test.requests {
				t.Fatalf("verdict %+v after %d requests", v, len(test.model.requests))
			}
			if !strings.Contains(v.Reason, test.reasonHas) {
				t.Fatalf("reason %q lacks %q", v.Reason, test.reasonHas)
			}
			if v.Classifier != "fake@"+PromptVersion || v.Usage.TotalTokens != int64(105*test.requests) {
				t.Fatalf("label/usage = %q %+v", v.Classifier, v.Usage)
			}
		})
	}
}

func TestClassifyStageParameters(t *testing.T) {
	model := &fakeModel{fast: "BLOCK", reason: `{"decision":"allow","rule":"","reason":""}`}
	if _, err := newTestClassifier(t, model).Classify(t.Context(), testRequest()); err != nil {
		t.Fatal(err)
	}
	fast, reason := model.requests[0], model.requests[1]
	if fast.Thinking != llm.ThinkingLevelOff || fast.MaxTokens != fastMaxTokens || reason.Thinking != llm.ThinkingLevelLow || reason.MaxTokens != reasonMaxTokens {
		t.Fatalf("stage parameters = %+v / %+v", fast, reason)
	}
	// Both stages share the system prompt and request prefix for prompt caching.
	if fast.System != reason.System || !strings.HasPrefix(reason.Prompt, strings.TrimSuffix(fast.Prompt, fastInstruction)) {
		t.Fatal("stages do not share a prompt prefix")
	}
}

func TestClassifyTimeoutIsUnavailable(t *testing.T) {
	model := &fakeModel{fast: "ALLOW", delay: 200 * time.Millisecond}
	c, err := New(Config{Model: model, FastTimeout: 20 * time.Millisecond, ReasonTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Classify(t.Context(), testRequest())
	if err != nil || v.Decision != Unavailable {
		t.Fatalf("verdict %+v, %v; want unavailable", v, err)
	}
}

func TestClassifyCancellationReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	v, err := newTestClassifier(t, &fakeModel{fast: "ALLOW"}).Classify(ctx, testRequest())
	if !errors.Is(err, context.Canceled) || v.Decision == Allow {
		t.Fatalf("canceled classify = %+v, %v", v, err)
	}
}

func TestNewRequiresModel(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New accepted a nil model")
	}
}
