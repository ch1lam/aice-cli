package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type elicitationContextKey struct{}

// Legacy servers send elicitation/create during the call; current servers
// return input_required and the SDK retries the same call with the answer.
func TestElicitationAnswersLegacyAndCurrentProtocols(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	server := mcp.NewServer(&mcp.Implementation{Name: "elicit", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "legacy", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			answer, err := req.Session.Elicit(ctx, &mcp.ElicitParams{Message: "Who?", RequestedSchema: schema})
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer.Action + ":" + toString(answer.Content["name"])}}}, nil
		})
	var currentCalls int
	server.AddTool(&mcp.Tool{Name: "current", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			currentCalls++
			if len(req.Params.InputResponses) == 0 {
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"who": &mcp.ElicitParams{Message: "Who?", RequestedSchema: schema}}}, nil
			}
			answer := req.Params.InputResponses["who"].(*mcp.ElicitResult)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer.Action + ":" + toString(answer.Content["name"])}}, StructuredContent: json.RawMessage(`{"n":9007199254740993}`)}, nil
		})
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer host.Close()

	for _, tc := range []struct {
		name, tool, protocol string
		reply                ElicitationReply
		want                 string
	}{
		{"legacy accept", "legacy", "2025-11-25", ElicitationReply{Action: ElicitationAccept, Content: map[string]any{"name": "Ada"}}, "accept:Ada"},
		{"current accept", "current", "", ElicitationReply{Action: ElicitationAccept, Content: map[string]any{"name": "Ada"}}, "accept:Ada"},
		{"current decline drops content", "current", "", ElicitationReply{Action: ElicitationDecline, Content: map[string]any{"name": "leak"}}, "decline:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []Elicitation
			c, err := Open(t.Context(), Config{HTTP: &HTTPConfig{Endpoint: host.URL}, ConnectTimeout: 3 * time.Second, ProtocolVersion: tc.protocol,
				Elicit: func(ctx context.Context, request Elicitation) (ElicitationReply, error) {
					if ctx.Value(elicitationContextKey{}) != "run" {
						t.Error("elicitation did not receive the operation context")
					}
					asked = append(asked, request)
					return tc.reply, nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx := context.WithValue(t.Context(), elicitationContextKey{}, "run")
			result, err := c.Call(ctx, tc.tool, json.RawMessage(`{}`))
			if err != nil || result.State != llm.ExecutionReturned || len(result.Content) != 1 || result.Content[0].Text != tc.want {
				t.Fatalf("result = %#v, %v", result, err)
			}
			if len(asked) != 1 || asked[0].Message != "Who?" || !json.Valid(asked[0].Schema) {
				t.Fatalf("asked = %#v", asked)
			}
			if tc.tool == "current" && string(result.StructuredContent) != `{"n":9007199254740993}` {
				t.Fatalf("structured content came from the interim result: %s", result.StructuredContent)
			}
		})
	}
	if currentCalls != 4 {
		t.Fatalf("current tool calls = %d, want one interim and one completed call per case", currentCalls)
	}
}

func toString(value any) string {
	text, _ := value.(string)
	return text
}
