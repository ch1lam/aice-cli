package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// webSearchToolParam declares the provider's web_search server tool under
// the run's policy. BuildRequest only sets request.WebSearch for a model
// whose support accepts that policy.
func webSearchToolParam(model llm.Model, search llm.WebSearch) (anthropicsdk.ToolUnionParam, error) {
	if model.WebSearch == nil || model.WebSearch.Tool != llm.WebSearchToolStandard {
		return anthropicsdk.ToolUnionParam{}, fmt.Errorf("anthropic: model %q has no hosted web search", model.ID)
	}
	if len(search.AllowedDomains) > 0 && len(search.ExcludedDomains) > 0 {
		return anthropicsdk.ToolUnionParam{}, errors.New("anthropic: web_search accepts allowed or blocked domains, not both")
	}
	return anthropicsdk.ToolUnionParam{OfWebSearchTool20250305: &anthropicsdk.WebSearchTool20250305Param{
		AllowedDomains: search.AllowedDomains,
		BlockedDomains: search.ExcludedDomains,
	}}, nil
}

// serverToolBlock is the part of server_tool_use and its result blocks AICE
// reads; the blocks themselves are kept unchanged for replay.
type serverToolBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	Input     struct {
		Query string `json:"query"`
		URL   string `json:"url"`
	} `json:"input"`
	Content json.RawMessage `json:"content"`
}

// serverToolUse completes a streamed server_tool_use block: the start block
// with the input its deltas carried.
func serverToolUse(start string, input string) (llm.WebSearchCall, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(start), &fields); err != nil {
		return llm.WebSearchCall{}, fmt.Errorf("anthropic: decode server_tool_use: %w", err)
	}
	if input != "" {
		if !json.Valid([]byte(input)) {
			return llm.WebSearchCall{}, errors.New("anthropic: server_tool_use input is not valid JSON")
		}
		fields["input"] = json.RawMessage(input)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return llm.WebSearchCall{}, fmt.Errorf("anthropic: encode server_tool_use: %w", err)
	}
	var block serverToolBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return llm.WebSearchCall{}, fmt.Errorf("anthropic: decode server_tool_use: %w", err)
	}
	call := llm.WebSearchCall{ID: block.ID, Replay: []json.RawMessage{raw}}
	switch block.Name {
	case "web_search":
		call.Action = llm.WebSearchActionSearch
		if block.Input.Query != "" {
			call.Queries = []string{block.Input.Query}
		}
	case "web_fetch":
		call.Action, call.URL = llm.WebSearchActionOpenPage, block.Input.URL
	default:
		return llm.WebSearchCall{}, fmt.Errorf("anthropic: unsupported server tool %q", block.Name)
	}
	return call, nil
}

// addServerToolResult reads a web_search_tool_result or
// web_fetch_tool_result block into the call it answers.
func addServerToolResult(call *llm.WebSearchCall, raw string) error {
	var block serverToolBlock
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		return fmt.Errorf("anthropic: decode server tool result: %w", err)
	}
	call.Replay = append(call.Replay, json.RawMessage(raw))
	// Search results are a list; a failure or a fetched page is an object.
	var results []struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if json.Unmarshal(block.Content, &results) == nil {
		for _, result := range results {
			if result.URL != "" {
				call.Sources = append(call.Sources, llm.WebSearchSource{URL: result.URL, Title: result.Title})
			}
		}
		return nil
	}
	var single struct {
		Type      string `json:"type"`
		ErrorCode string `json:"error_code"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(block.Content, &single); err != nil {
		return fmt.Errorf("anthropic: decode %s content: %w", block.Type, err)
	}
	switch {
	case single.ErrorCode != "":
		call.Error = single.ErrorCode
	case single.URL != "":
		call.Sources = append(call.Sources, llm.WebSearchSource{URL: single.URL})
	}
	return nil
}

// webSearchBlockParams replays a recorded step to the same model in a
// request that declares the server tool; any other request drops it, keeping
// the answer text it informed.
func webSearchBlockParams(part llm.ContentPart, replay bool) ([]anthropicsdk.ContentBlockParamUnion, error) {
	if !replay || part.WebSearch == nil {
		return nil, nil
	}
	blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, len(part.WebSearch.Replay))
	for _, raw := range part.WebSearch.Replay {
		var block serverToolBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, fmt.Errorf("decode web search replay item: %w", err)
		}
		switch block.Type {
		case "server_tool_use":
			converted := param.Override[anthropicsdk.ServerToolUseBlockParam](raw)
			blocks = append(blocks, anthropicsdk.ContentBlockParamUnion{OfServerToolUse: &converted})
		case "web_search_tool_result":
			converted := param.Override[anthropicsdk.WebSearchToolResultBlockParam](raw)
			blocks = append(blocks, anthropicsdk.ContentBlockParamUnion{OfWebSearchToolResult: &converted})
		case "web_fetch_tool_result":
			converted := param.Override[anthropicsdk.WebFetchToolResultBlockParam](raw)
			blocks = append(blocks, anthropicsdk.ContentBlockParamUnion{OfWebFetchToolResult: &converted})
		default:
			// Another protocol's item: nothing this API can receive.
			return nil, nil
		}
	}
	return blocks, nil
}
