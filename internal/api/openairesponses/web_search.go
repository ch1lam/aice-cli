package openairesponses

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// webSearchToolParam declares the model's hosted search tool under the run's
// policy. BuildRequest only sets request.WebSearch for a model whose support
// accepts that policy, so a list the tool cannot carry never reaches here.
func webSearchToolParam(model llm.Model, search llm.WebSearch) (responses.ToolUnionParam, error) {
	if model.WebSearch == nil {
		return responses.ToolUnionParam{}, fmt.Errorf("openai responses: model %q has no hosted web search", model.ID)
	}
	switch model.WebSearch.Tool {
	case llm.WebSearchToolStandard:
		if len(search.ExcludedDomains) > 0 {
			return responses.ToolUnionParam{}, fmt.Errorf("openai responses: web_search cannot exclude domains")
		}
		tool := responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch)
		if len(search.AllowedDomains) > 0 {
			tool.OfWebSearch.Filters = responses.WebSearchToolFiltersParam{AllowedDomains: search.AllowedDomains}
		}
		return tool, nil
	case llm.WebSearchToolDeepSeek:
		if len(search.AllowedDomains) > 0 || len(search.ExcludedDomains) > 0 {
			return responses.ToolUnionParam{}, fmt.Errorf("openai responses: deepseek_web_search cannot restrict domains")
		}
		tool := param.Override[responses.WebSearchToolParam](json.RawMessage(`{"type":"deepseek_web_search"}`))
		return responses.ToolUnionParam{OfWebSearch: &tool}, nil
	default:
		return responses.ToolUnionParam{}, fmt.Errorf("openai responses: unsupported hosted web search tool %q", model.WebSearch.Tool)
	}
}

// webSearchCallItem is the part of a web_search_call output item AICE reads.
// DeepSeek adds titles, pages and error codes beside OpenAI's fields; the
// item itself is kept unchanged for replay.
type webSearchCallItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Action struct {
		Type    string   `json:"type"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		URL     string   `json:"url"`
		Sources []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"sources"`
	} `json:"action"`
	Page struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"page"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// webSearchCall reads a completed web_search_call item.
func webSearchCall(raw string) (llm.WebSearchCall, error) {
	var item webSearchCallItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return llm.WebSearchCall{}, fmt.Errorf("openai responses: decode web_search_call: %w", err)
	}
	call := llm.WebSearchCall{
		ID:      item.ID,
		Action:  llm.WebSearchAction(item.Action.Type),
		Queries: item.Action.Queries,
		URL:     item.Action.URL,
		Error:   item.Error.Code,
		Replay:  []json.RawMessage{json.RawMessage(raw)},
	}
	if len(call.Queries) == 0 && item.Action.Query != "" {
		call.Queries = []string{item.Action.Query}
	}
	for _, source := range item.Action.Sources {
		if source.URL != "" {
			call.Sources = append(call.Sources, llm.WebSearchSource{URL: source.URL, Title: source.Title})
		}
	}
	if item.Page.URL != "" {
		call.Sources = append(call.Sources, llm.WebSearchSource{URL: item.Page.URL, Title: item.Page.Title})
	}
	if call.Error == "" && item.Status == string(responses.ResponseFunctionWebSearchStatusFailed) {
		call.Error = "failed"
	}
	return call, nil
}

// webSearchInputParams replays a recorded step to the same model; any other
// request drops it, keeping the answer text it informed.
func webSearchInputParams(part llm.ContentPart, replay bool) []responses.ResponseInputItemUnionParam {
	if !replay || part.WebSearch == nil {
		return nil
	}
	items := make([]responses.ResponseInputItemUnionParam, 0, len(part.WebSearch.Replay))
	for _, raw := range part.WebSearch.Replay {
		item := param.Override[responses.ResponseFunctionWebSearchParam](raw)
		items = append(items, responses.ResponseInputItemUnionParam{OfWebSearchCall: &item})
	}
	return items
}

// deepseekReservedNames are function tool names the API rejects beside
// deepseek_web_search (checked 2026-11-27); they name its own actions.
var deepseekReservedNames = []string{"find", "search", "open", "deepseek_web_search"}

// toolNames renames function tools on the wire for one request. Only tools
// whose names a hosted tool reserves are renamed; history and streamed calls
// use the same mapping, so the Agent Loop sees only the request's names.
type toolNames struct {
	renamed  map[string]string // request name to wire name
	original map[string]string // wire name to request name
}

func wireToolNames(request llm.Request) toolNames {
	if request.WebSearch == nil || request.Model.WebSearch == nil || request.Model.WebSearch.Tool != llm.WebSearchToolDeepSeek {
		return toolNames{}
	}
	taken := make(map[string]bool, len(request.Tools))
	for _, tool := range request.Tools {
		taken[tool.Name] = true
	}
	var names toolNames
	for _, tool := range request.Tools {
		if !slices.Contains(deepseekReservedNames, tool.Name) {
			continue
		}
		wire := "aice_" + tool.Name
		for taken[wire] || slices.Contains(deepseekReservedNames, wire) {
			wire += "_"
		}
		taken[wire] = true
		if names.renamed == nil {
			names.renamed, names.original = map[string]string{}, map[string]string{}
		}
		names.renamed[tool.Name], names.original[wire] = wire, tool.Name
	}
	return names
}

func (n toolNames) wire(name string) string {
	if wire, ok := n.renamed[name]; ok {
		return wire
	}
	return name
}

func (s *stream) toolName(wire string) string {
	if name, ok := s.toolNames[wire]; ok {
		return name
	}
	return wire
}
