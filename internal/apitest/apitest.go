// Package apitest contains shared test helpers for protocol adapter tests.
package apitest

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// CollectEvents drains a model stream until EOF, failing the test on errors.
func CollectEvents(t *testing.T, stream llm.Stream) []llm.Event {
	t.Helper()

	var events []llm.Event
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		events = append(events, event)
	}
}

// NewSSEServer serves each event as an SSE data frame.
func NewSSEServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
}

// NewRawSSEServer serves raw SSE frames with escaped newlines.
func NewRawSSEServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, strings.ReplaceAll(chunk, `\n`, "\n"))
		}
	}))
}

// RoundTripFunc adapts a function to http.RoundTripper.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// MinimalRequest builds the smallest valid request for a protocol API.
func MinimalRequest(api llm.API) llm.Request {
	return llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-flash",
			API:       api,
			Provider:  "deepseek",
			MaxTokens: 1_000,
		},
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	}
}
