package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

type recordingStreamer struct{ requests []llm.Request }

func (s *recordingStreamer) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	s.requests = append(s.requests, request)
	return nil, errors.New("recorded")
}

func TestRouteGate(t *testing.T) {
	t.Parallel()

	text := llm.Model{ID: "text", Provider: "test", API: "api-a", InputModalities: []llm.InputModality{llm.InputModalityText}}
	vision := llm.Model{ID: "vision", Provider: "test", API: "api-b", InputModalities: []llm.InputModality{llm.InputModalityText, llm.InputModalityImage}}
	unrouted := llm.Model{ID: "unrouted", Provider: "test", API: "api-c"}
	image := llm.ContentPart{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}
	redacted := llm.ContentPart{Type: llm.ContentTypeThinking, Text: "opaque", Redacted: true}
	nested := llm.ContentPart{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResult{CallID: "call-1", Content: []llm.ContentPart{image}}}
	user := func(parts ...llm.ContentPart) []llm.Message {
		return []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: parts}}
	}
	toolResult := []llm.Message{llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-1", Content: []llm.ContentPart{image}}}

	tests := []struct {
		name     string
		model    llm.Model
		messages []llm.Message
		nested   bool
		want     string // empty means the request reaches the adapter
	}{
		{name: "dispatch by API", model: vision},
		{name: "provider mismatch", model: llm.Model{ID: "text", Provider: "other", API: "api-a"}, want: `test: model provider "other" does not match "test"`},
		{name: "unknown model", model: llm.Model{ID: "missing", Provider: "test", API: "api-a"}, want: `test: unsupported model "missing"`},
		{name: "blank model", model: llm.Model{ID: " ", Provider: "test", API: "api-a"}, want: `test: unsupported model " "`},
		{name: "API mismatch", model: llm.Model{ID: "text", Provider: "test", API: "api-b"}, want: `test: model "text" API "api-b" does not match "api-a"`},
		{name: "API without adapter", model: unrouted, want: `test: model "unrouted" uses unsupported API "api-c"`},
		{name: "image on text model", model: text, messages: user(image), want: "test: message 0 content 0: image content is not supported by Test models"},
		{name: "image on vision model", model: vision, messages: user(image)},
		{name: "image tool result on text model", model: text, messages: toolResult, want: "test: message 0 content 0: non-text tool results are not supported by Test models"},
		{name: "image tool result on vision model", model: vision, messages: toolResult},
		{name: "redacted thinking on listed API", model: text, messages: user(redacted)},
		{name: "redacted thinking on other API", model: vision, messages: user(redacted), want: "test: message 0 content 0: redacted thinking is not supported by Test models"},
		{name: "nested image allowed without text-only rule", model: text, messages: user(nested)},
		{name: "nested image on text-only text model", model: text, messages: user(nested), nested: true, want: "test: message 0 content 0: non-text tool results are not supported by Test models"},
		{name: "nested image on text-only vision model", model: vision, messages: user(nested), nested: true},
		{name: "nil message", model: text, messages: []llm.Message{nil}, want: "test: message 0 is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, b := &recordingStreamer{}, &recordingStreamer{}
			route := provider.Route{
				Provider:                 "test",
				Label:                    "Test",
				Models:                   []llm.Model{text, vision, unrouted},
				Adapters:                 map[llm.API]llm.Streamer{"api-a": a, "api-b": b},
				RedactedThinking:         []llm.API{"api-a"},
				NestedToolResultTextOnly: tt.nested,
			}
			messages := tt.messages
			if messages == nil {
				messages = user(llm.NewTextContent("hello").Part())
			}
			_, err := route.Stream(t.Context(), llm.Request{Model: tt.model, Messages: messages})
			if tt.want != "" {
				if err == nil || err.Error() != tt.want {
					t.Fatalf("Stream() error = %v, want %q", err, tt.want)
				}
				if len(a.requests)+len(b.requests) != 0 {
					t.Fatal("rejected request reached an adapter")
				}
				return
			}
			want := map[llm.API]*recordingStreamer{"api-a": a, "api-b": b}[tt.model.API]
			if len(want.requests) != 1 || len(a.requests)+len(b.requests) != 1 {
				t.Fatalf("adapter requests = %d/%d, want one for %s", len(a.requests), len(b.requests), tt.model.API)
			}
		})
	}
}

func TestRouteModelForIDDescribesUncataloguedModels(t *testing.T) {
	t.Parallel()

	adapter := &recordingStreamer{}
	route := provider.Route{
		Provider: "test",
		Label:    "Test",
		ModelForID: func(id string) llm.Model {
			return llm.Model{ID: id, Provider: "test", API: "api", InputModalities: []llm.InputModality{llm.InputModalityText}}
		},
		Adapters: map[llm.API]llm.Streamer{"api": adapter},
	}
	request := llm.Request{
		Model:    llm.Model{ID: "any-local-model", Provider: "test", API: "api"},
		Messages: []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("hi").Part()}}},
	}
	if _, err := route.Stream(t.Context(), request); err == nil || len(adapter.requests) != 1 {
		t.Fatalf("Stream() error = %v, requests = %d, want adapter dispatch", err, len(adapter.requests))
	}
	request.Messages = []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{{
		Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("x"), MIMEType: "image/png"},
	}}}}
	if err := route.Check(request); err == nil {
		t.Fatal("Check() accepted an image for a text-only described model")
	}
}
