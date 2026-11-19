package apitest_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// adapterCase builds one protocol adapter against a test endpoint.
type adapterCase struct {
	api llm.API
	new func(baseURL string, client *http.Client) (llm.Streamer, error)
	// checkWire adds protocol-specific assertions on a tool-image request body.
	checkWire func(t *testing.T, body []byte)
}

func adapterCases() []adapterCase {
	return []adapterCase{
		{api: anthropic.API, new: func(baseURL string, client *http.Client) (llm.Streamer, error) {
			return anthropic.New(anthropic.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
		}},
		{api: openairesponses.API, new: func(baseURL string, client *http.Client) (llm.Streamer, error) {
			return openairesponses.New(openairesponses.Config{
				APIKey: "test-key", BaseURL: baseURL, HTTPClient: client,
				Headers: http.Header{"User-Agent": {"generic-sdk/1.0"}},
			})
		}},
		{api: openaicompletions.API, new: func(baseURL string, client *http.Client) (llm.Streamer, error) {
			return openaicompletions.New(openaicompletions.Config{APIKey: "test-key", BaseURL: baseURL, HTTPClient: client})
		}, checkWire: checkCompletionsToolGroup},
	}
}

// requestBody streams messages through the adapter and returns the encoded
// request without completing it.
func requestBody(t *testing.T, tc adapterCase, messages []llm.Message) []byte {
	t.Helper()
	var body []byte
	client := &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		return nil, errors.New("request captured")
	})}
	adapter, err := tc.new("https://example.test", client)
	if err != nil {
		t.Fatal(err)
	}
	model := llm.Model{ID: "vision", Provider: "fixture", API: tc.api, MaxTokens: 1_000}
	if _, err := adapter.Stream(t.Context(), llm.Request{Model: model, Messages: messages}); err == nil {
		t.Fatal("Stream() succeeded without a response")
	}
	if body == nil {
		t.Fatal("request never reached HTTP")
	}
	return body
}

func TestAdaptersSendVersionedClientIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range adapterCases() {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Parallel()
			received := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			adapter, err := tc.new(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			stream, err := adapter.Stream(t.Context(), apitest.MinimalRequest(tc.api))
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			want := "aice/" + buildinfo.Version
			if values := (<-received).Values("User-Agent"); len(values) != 1 || values[0] != want {
				t.Fatalf("User-Agent = %q, want a single %q", values, want)
			}
		})
	}
}

// TestAdaptersProjectStructuredToolResults sends a structured error result
// that interleaves text and images; content and outcome notes keep their
// order, the binding stays private, and the source history is unchanged.
func TestAdaptersProjectStructuredToolResults(t *testing.T) {
	t.Parallel()
	for _, tc := range adapterCases() {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Parallel()
			result := llm.ToolResultMessage{
				Role: llm.RoleToolResult, ToolCallID: "call-one", ToolName: "inspect", IsError: true,
				Content: []llm.ContentPart{
					llm.NewTextContent("before-image-one").Part(),
					imagePart(llm.ImageContent{Data: []byte("image-one-bytes"), MIMEType: "image/png"}),
					llm.NewTextContent("between-images").Part(),
					imagePart(llm.ImageContent{Data: []byte("image-two-bytes"), MIMEType: "image/png"}),
					llm.NewTextContent("after-image-two").Part(),
				},
				Details: &llm.ToolResultDetails{
					State:             llm.ExecutionUnknown,
					StructuredContent: json.RawMessage(`{"count":9007199254740993}`),
					Loss:              "one block exceeded limit",
					Binding: &llm.ToolBinding{
						Source: "fixture-source", ServiceID: "fixture-id", ConnectionFingerprint: "private-connection-fingerprint",
						ToolName: "raw-tool", SchemaFingerprint: "private-schema-fingerprint",
					},
				},
			}
			messages := []llm.Message{toolCalls(tc.api, "call-one"), result}
			text := string(requestBody(t, tc, messages))
			previous := -1
			for _, needle := range []string{
				"before-image-one", base64.StdEncoding.EncodeToString([]byte("image-one-bytes")),
				"between-images", base64.StdEncoding.EncodeToString([]byte("image-two-bytes")),
				"after-image-two", "9007199254740993", "outcome unknown", "cannot be recovered",
			} {
				position := strings.Index(text, needle)
				if position <= previous {
					t.Fatalf("missing or reordered %q: %s", needle, text)
				}
				previous = position
			}
			if strings.Contains(text, "private-connection-fingerprint") || strings.Contains(text, "private-schema-fingerprint") {
				t.Fatal("binding leaked into provider input")
			}
			if source := messages[1].(llm.ToolResultMessage); len(source.Content) != 5 || source.Content[2].Text != "between-images" {
				t.Fatal("source result mutated")
			}
		})
	}
}

// TestAdaptersSendToolImageViewOnly sends a converted image view: its bytes,
// identity and conversion notes reach the wire, the original bytes never do.
func TestAdaptersSendToolImageViewOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range adapterCases() {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Parallel()
			image := llm.ImageContent{Data: []byte("view"), MIMEType: "image/png", ID: "image:test", Width: 2, Height: 2,
				Original: &llm.ImageOriginal{Data: []byte("original-secret-bytes"), MIMEType: "image/gif", Width: 4, Height: 4}}
			body := requestBody(t, tc, []llm.Message{
				toolCalls(tc.api, "one", "two"),
				llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "one", ToolName: "read", Content: []llm.ContentPart{imagePart(image)}},
				llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "two", ToolName: "read", Content: []llm.ContentPart{llm.NewTextContent("second result").Part()}},
			})
			text := string(body)
			if !strings.Contains(text, base64.StdEncoding.EncodeToString(image.Data)) || !strings.Contains(text, "image:test") {
				t.Fatal("image bytes or identity missing from wire")
			}
			if !strings.Contains(text, "converted from image/gif to image/png") || !strings.Contains(text, "first frame only") {
				t.Fatal("conversion and animation policy missing from wire")
			}
			if strings.Contains(text, base64.StdEncoding.EncodeToString(image.Original.Data)) || strings.Contains(text, "original-secret-bytes") {
				t.Fatal("original bytes leaked into request")
			}
			if tc.checkWire != nil {
				tc.checkWire(t, body)
			}
		})
	}
}

// checkCompletionsToolGroup requires the text-only tool messages to stay
// contiguous, with the image-bearing user message after the whole group.
func checkCompletionsToolGroup(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	m := request.Messages
	if len(m) != 4 || m[1].Role != "tool" || m[1].ToolCallID != "one" || m[2].Role != "tool" || m[2].ToolCallID != "two" || m[3].Role != "user" {
		t.Fatalf("tool group interrupted: %s", body)
	}
}

func toolCalls(api llm.API, ids ...string) llm.AssistantMessage {
	message := llm.AssistantMessage{
		Role: llm.RoleAssistant, API: api, Provider: "fixture", ModelID: "vision", StopReason: llm.StopReasonToolUse,
	}
	for _, id := range ids {
		message.Content = append(message.Content, llm.ContentPart{
			Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: id, Name: "read", Arguments: json.RawMessage(`{}`)},
		})
	}
	return message
}

func imagePart(image llm.ImageContent) llm.ContentPart {
	return llm.ContentPart{Type: llm.ContentTypeImage, Image: &image}
}
