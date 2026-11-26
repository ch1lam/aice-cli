package kimi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/kimi"
)

func TestProviderDispatchesThroughResponsesAPI(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Kimi bearer key")
		}
		if r.UserAgent() != "aice/"+buildinfo.Version {
			t.Errorf("User-Agent = %s", r.UserAgent())
		}
		var body struct {
			Model           string `json:"model"`
			Stream          bool   `json:"stream"`
			Store           bool   `json:"store"`
			MaxOutputTokens int64  `json:"max_output_tokens"`
			Reasoning       struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		wantMaxTokens := int64(131_072)
		if body.Model == kimi.ModelForCoding || body.Model == kimi.ModelForCodingHighSpeed {
			wantMaxTokens = 32_768
		}
		if body.Model == "" || !body.Stream || body.Store || body.Reasoning.Effort != "high" || body.MaxOutputTokens != wantMaxTokens {
			t.Errorf("request = %+v, want max_output_tokens %d", body, wantMaxTokens)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	modelProvider, err := kimi.New(kimi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/coding/v1/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, candidate := range kimi.Models() {
		t.Run(candidate.ID, func(t *testing.T) {
			request := llm.Request{
				Model:   candidate,
				Options: llm.StreamOptions{Thinking: llm.ClampThinkingLevel(candidate, llm.ThinkingLevelMedium)},
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}},
			}
			stream, err := modelProvider.Stream(context.Background(), request)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if err := stream.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			if got := <-paths; got != "/coding/v1/responses" {
				t.Errorf("request path = %q, want /responses", got)
			}
		})
	}
}

func TestKimiForCodingSupportsThinkingOffAsNone(t *testing.T) {
	t.Parallel()

	var gotEffort string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		gotEffort = body.Reasoning.Effort
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	modelProvider, err := kimi.New(kimi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/coding/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := modelProvider.Stream(context.Background(), llm.Request{
		Model:   kimi.DefaultModel(),
		Options: llm.StreamOptions{Thinking: llm.ThinkingLevelOff},
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if gotEffort != "none" {
		t.Errorf("effort = %q, want none", gotEffort)
	}
}
