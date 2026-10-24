package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const elicitationTestSchema = `{"type":"object","required":["name","size"],"properties":{
	"name":{"type":"string","title":"Name"},
	"size":{"type":"string","enum":["s","m","l"],"enumNames":["Small","Medium","Large"]},
	"count":{"type":"integer"},
	"notify":{"type":"boolean"}}}`

// answerQuestions answers every question of each page with answer.
func answerQuestions(t *testing.T, requests chan interaction.QuestionPrompt, answer func(interaction.QuestionItem) interaction.QuestionAnswer) {
	t.Helper()
	go func() {
		for prompt := range requests {
			reply := interaction.QuestionReply{RequestID: prompt.Request.ID, Answers: make(map[string]interaction.QuestionAnswer)}
			for _, question := range prompt.Request.Questions {
				reply.Answers[question.ID] = answer(question)
			}
			prompt.Reply <- reply
		}
	}()
}

func TestMCPElicitationMapsFormFieldsToQuestionPages(t *testing.T) {
	for _, tc := range []struct {
		name string
		skip string
		want mcpclient.ElicitationReply
	}{
		{"accept", "", mcpclient.ElicitationReply{Action: mcpclient.ElicitationAccept, Content: map[string]any{"count": int64(3), "name": "Ada", "notify": true, "size": "m"}}},
		{"optional skip", "Count", mcpclient.ElicitationReply{Action: mcpclient.ElicitationAccept, Content: map[string]any{"name": "Ada", "notify": true, "size": "m"}}},
		{"required skip declines", "Name", mcpclient.ElicitationReply{Action: mcpclient.ElicitationDecline}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan interaction.QuestionPrompt, 4)
			defer close(requests)
			session := &interactiveSession{questionRequests: requests}
			answerQuestions(t, requests, func(q interaction.QuestionItem) interaction.QuestionAnswer {
				label := q.Question[strings.LastIndex(q.Question, "\n")+1:]
				switch {
				case tc.skip != "" && strings.HasPrefix(label, tc.skip):
					return interaction.QuestionAnswer{Status: interaction.QuestionSkipped}
				case strings.HasPrefix(label, "Name"):
					return interaction.QuestionAnswer{Status: interaction.QuestionAnswered, Text: "Ada"}
				case strings.HasPrefix(label, "Count"), strings.HasPrefix(label, "count"):
					return interaction.QuestionAnswer{Status: interaction.QuestionAnswered, Text: " 3 "}
				case strings.HasPrefix(label, "notify"):
					return interaction.QuestionAnswer{Status: interaction.QuestionAnswered, SelectedOptionID: "v0", SelectedLabel: "Yes"}
				case strings.HasPrefix(label, "size"):
					return interaction.QuestionAnswer{Status: interaction.QuestionAnswered, SelectedOptionID: "v1", SelectedLabel: "Medium"}
				}
				t.Errorf("unexpected question %q", q.Question)
				return interaction.QuestionAnswer{Status: interaction.QuestionSkipped}
			})
			schema := strings.ReplaceAll(elicitationTestSchema, `"count":{"type":"integer"}`, `"count":{"type":"integer","title":"Count"}`)
			got, err := session.elicitMCP(t.Context(), "user:docs", mcpclient.Elicitation{Message: "Order details", Schema: json.RawMessage(schema)})
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("reply = %#v, %v", got, err)
			}
		})
	}
	session := &interactiveSession{}
	if got, err := session.elicitMCP(t.Context(), "user:docs", mcpclient.Elicitation{Schema: json.RawMessage(elicitationTestSchema)}); err != nil || got.Action != mcpclient.ElicitationCancel {
		t.Fatalf("no frontend = %#v, %v", got, err)
	}
	if _, err := parseMCPElicitationSchema(json.RawMessage(`{"type":"object","properties":{"nested":{"type":"object"}}}`)); err == nil {
		t.Fatal("nested schema accepted")
	}
}

// Through the owner and a real current-protocol server: the run's elicitor
// answers; without one (Print) the request is cancelled.
func TestMCPOwnerRoutesElicitationToTheRun(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "elicit", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "ask", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if len(req.Params.InputResponses) == 0 {
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"who": &mcp.ElicitParams{Message: "Who?", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)}}}, nil
			}
			answer := req.Params.InputResponses["who"].(*mcp.ElicitResult)
			name, _ := answer.Content["name"].(string)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer.Action + ":" + name}}}, nil
		})
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer host.Close()
	c := ownerTestConfig(t, 0)
	c, err := c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": {Transport: "http", URL: host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
	owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	connection := owner.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context) string {
		result, err := connection.CallChecked(ctx, "ask", json.RawMessage(`{}`), func(context.Context) error { return nil })
		if err != nil || len(result.Content) != 1 {
			t.Fatalf("call = %#v, %v", result, err)
		}
		return result.Content[0].Text
	}
	var service string
	run := withMCPElicitor(t.Context(), func(_ context.Context, key string, request mcpclient.Elicitation) (mcpclient.ElicitationReply, error) {
		service = key
		return mcpclient.ElicitationReply{Action: mcpclient.ElicitationAccept, Content: map[string]any{"name": "Ada"}}, nil
	})
	if got := call(run); got != "accept:Ada" || service != "user:service0" {
		t.Fatalf("interactive answer = %q from %q", got, service)
	}
	if got := call(t.Context()); got != "cancel:" {
		t.Fatalf("non-interactive answer = %q", got)
	}
}
