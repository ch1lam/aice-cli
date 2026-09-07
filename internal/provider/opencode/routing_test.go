package opencode_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestRoutingIdentityAcrossModelsAndConversations(t *testing.T) {
	t.Parallel()
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	client := server.Client()
	originalTransport := client.Transport
	p, err := opencode.New(opencode.Config{APIKey: "test-key", BaseURL: server.URL + "/v1", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if client.Transport != originalTransport {
		t.Fatal("provider mutated caller's HTTP client")
	}
	fallback := ""
	for _, id := range []string{"", "conversation-a", "conversation-b", "conversation-a", ""} {
		for _, model := range opencode.Models() {
			stream, err := p.Stream(llm.WithSessionID(t.Context(), id), llm.Request{
				Model: model,
				Messages: []llm.Message{llm.UserMessage{
					Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}},
			})
			if err != nil {
				t.Fatalf("%s: %v", model.ID, err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			header := <-headers
			got := header.Get("x-opencode-session")
			want := id
			if id == "" {
				if fallback == "" {
					fallback = got
				}
				want = fallback
			}
			if got == "" || got != want {
				t.Fatalf("%s: routing ID = %q, want %q", model.ID, got, want)
			}
			if header.Get("User-Agent") != "aice" {
				t.Fatalf("User-Agent = %q", header.Get("User-Agent"))
			}
		}
	}
}
