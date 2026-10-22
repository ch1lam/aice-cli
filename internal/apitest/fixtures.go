package apitest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// ToolResultHistory returns a same-model tool call followed by its structured
// error result. The result interleaves text and images, carries structured
// content and loss details, and binds private fingerprints that must never
// reach a provider.
func ToolResultHistory(api llm.API) (llm.Model, []llm.Message) {
	model := llm.Model{ID: "vision", Provider: "fixture", API: api}
	result := llm.ToolResultMessage{
		Role: llm.RoleToolResult, ToolCallID: "call-one", ToolName: "inspect", IsError: true,
		Content: []llm.ContentPart{
			llm.NewTextContent("before-image-one").Part(),
			{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image-one-bytes"), MIMEType: "image/png"}},
			llm.NewTextContent("between-images").Part(),
			{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image-two-bytes"), MIMEType: "image/png"}},
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
	call := llm.AssistantMessage{
		Role: llm.RoleAssistant, API: api, Provider: model.Provider, ModelID: model.ID, StopReason: llm.StopReasonToolUse,
		Content: []llm.ContentPart{{
			Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "call-one", Name: "inspect", Arguments: json.RawMessage(`{}`)},
		}},
	}
	return model, []llm.Message{call, result}
}

// CheckToolResultProjection verifies that converted, the adapter's wire
// parameters for ToolResultHistory, keeps content and outcome notes in order,
// omits the binding, and leaves the source history unchanged.
func CheckToolResultProjection(t *testing.T, converted any, history []llm.Message) {
	t.Helper()
	wire, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
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
	result := history[len(history)-1].(llm.ToolResultMessage)
	if len(result.Content) != 5 || result.Content[2].Text != "between-images" {
		t.Fatal("source result mutated")
	}
}

// CheckClientIdentity streams a minimal request through the adapter built by
// newAdapter and requires exactly one AICE User-Agent on the wire.
func CheckClientIdentity(
	t *testing.T,
	api llm.API,
	newAdapter func(baseURL string, client *http.Client) (llm.Streamer, error),
) {
	t.Helper()
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	adapter, err := newAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(t.Context(), MinimalRequest(api))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	header := <-received
	want := "aice/" + buildinfo.Version
	if values := header.Values("User-Agent"); len(values) != 1 || values[0] != want {
		t.Fatalf("User-Agent = %q, want a single %q", values, want)
	}
}
