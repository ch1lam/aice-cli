package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWebSearchPartRoundTripsAndValidates(t *testing.T) {
	t.Parallel()
	call := WebSearchCall{ID: "ws_1", Action: WebSearchActionSearch, Queries: []string{"q"},
		Sources: []WebSearchSource{{URL: "https://go.dev", Title: "Go"}},
		Replay:  []json.RawMessage{json.RawMessage(`{"type":"web_search_call","id":"ws_1"}`)}}
	message := AssistantMessage{Role: RoleAssistant, API: "a", Provider: "p", ModelID: "m", StopReason: StopReasonStop,
		Content: []ContentPart{NewWebSearchPart(call), NewTextContent("answer").Part()}}
	data, err := MarshalAgentMessages([]AgentMessage{message})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalAgentMessages(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded[0].(AssistantMessage).Content[0].WebSearch; !reflect.DeepEqual(*got, call) {
		t.Fatalf("round trip = %+v", got)
	}

	cloned := cloneContentParts(message.Content)
	cloned[0].WebSearch.Replay[0][2] = 'X'
	cloned[0].WebSearch.Queries[0] = "changed"
	if string(message.Content[0].WebSearch.Replay[0]) != `{"type":"web_search_call","id":"ws_1"}` || message.Content[0].WebSearch.Queries[0] != "q" {
		t.Fatal("clone aliases the original")
	}

	for name, part := range map[string]ContentPart{
		"no payload":     {Type: ContentTypeWebSearch},
		"no id":          NewWebSearchPart(WebSearchCall{Replay: call.Replay}),
		"no replay":      NewWebSearchPart(WebSearchCall{ID: "ws"}),
		"invalid replay": NewWebSearchPart(WebSearchCall{ID: "ws", Replay: []json.RawMessage{json.RawMessage(`{`)}}),
		"mixed payload":  {Type: ContentTypeWebSearch, Text: "x", WebSearch: &call},
	} {
		if part.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	user := UserMessage{Role: RoleUser, Content: []ContentPart{NewWebSearchPart(call)}}
	if user.Validate() == nil {
		t.Fatal("user message accepted hosted search content")
	}
}

func TestWebSearchSupportAccepts(t *testing.T) {
	t.Parallel()
	both := WebSearch{AllowedDomains: []string{"a.com"}, ExcludedDomains: []string{"b.com"}}
	anthropic := &WebSearchSupport{Tool: WebSearchToolStandard, AllowedDomains: true, ExcludedDomains: true, SingleDomainList: true}
	if anthropic.Accepts(both, ThinkingLevelOff) || !anthropic.Accepts(WebSearch{ExcludedDomains: both.ExcludedDomains}, ThinkingLevelOff) {
		t.Fatal("single domain list rule")
	}
	var none *WebSearchSupport
	if none.Accepts(WebSearch{}, ThinkingLevelHigh) {
		t.Fatal("nil support accepted")
	}
	deepseek := &WebSearchSupport{Tool: WebSearchToolDeepSeek, RequiresThinking: true}
	if deepseek.Accepts(WebSearch{}, ThinkingLevelUnknown) || !deepseek.Accepts(WebSearch{}, ThinkingLevelLow) {
		t.Fatal("thinking rule")
	}
}
