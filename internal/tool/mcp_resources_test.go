package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type resourceListFixture struct{ calls int }

func (f *resourceListFixture) ListResources(context.Context, ResourceListRequest) (ResourceListResult, error) {
	f.calls++
	return ResourceListResult{Service: "user:docs", Complete: true, Secrets: []string{"private-key"}, Resources: []mcpclient.Resource{{URI: "fixture://private-key", Name: "hidden"}, {URI: "fixture://safe", Name: "private-key", Description: strings.Repeat("中文🙂", 1000)}}}, nil
}
func TestMCPResourceListBoundsAndCredentialRedaction(t *testing.T) {
	fixture := &resourceListFixture{}
	list, _ := NewMCPResourceList(fixture)
	call := llm.ToolCall{ID: "list", Name: "mcp_resource_list", Arguments: []byte(`{"service":"user:docs"}`)}
	result, selected, err := list.SelectTools(t.Context(), call)
	if err != nil || result.IsError || len(selected) != 0 {
		t.Fatal(result, err)
	}
	text := result.Content[0].Text
	if strings.Contains(text, "private-key") || !strings.Contains(text, "fixture://safe") || !strings.Contains(text, "omitted") || len(text) > 2500 {
		t.Fatal("resource metadata not safely bounded", len(text))
	}
	for _, args := range []string{`{}`, `{"service":"user:docs","offset":-1}`, `{"service":"user:docs","limit":6}`, `{"service":"user:docs","path":"/secret"}`} {
		call.Arguments = []byte(args)
		result, _, err := list.SelectTools(t.Context(), call)
		if err != nil || !result.IsError {
			t.Fatal("invalid resource request accepted", args)
		}
	}
	if fixture.calls != 1 {
		t.Fatal("invalid arguments reached backend")
	}
}
