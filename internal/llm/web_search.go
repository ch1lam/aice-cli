package llm

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// WebSearchTool names the provider-hosted search tool a model accepts. The
// protocol adapter owns its wire form.
type WebSearchTool string

const (
	// WebSearchToolStandard is the protocol's own tool: web_search on the
	// Responses API, web_search_20250305 on Anthropic Messages.
	WebSearchToolStandard WebSearchTool = "standard"
	// WebSearchToolDeepSeek is DeepSeek's deepseek_web_search on the
	// Responses API.
	WebSearchToolDeepSeek WebSearchTool = "deepseek"
)

// WebSearchSupport declares a catalog model's provider-hosted web search.
// The search runs on the provider's servers during a response; AICE only
// enables it and records what the provider reports.
type WebSearchSupport struct {
	Tool WebSearchTool `json:"tool"`
	// RequiresThinking reports that the provider accepts the tool beside
	// function tools only while reasoning is enabled.
	RequiresThinking bool `json:"requires_thinking,omitempty"`
	// AllowedDomains and ExcludedDomains report which domain lists the
	// provider enforces. A policy list the tool cannot enforce disables
	// hosted search instead of being dropped.
	AllowedDomains  bool `json:"allowed_domains,omitempty"`
	ExcludedDomains bool `json:"excluded_domains,omitempty"`
	// SingleDomainList reports that the two lists cannot be sent together.
	SingleDomainList bool `json:"single_domain_list,omitempty"`
}

// Accepts reports whether the model's hosted search can serve search under
// the requested thinking level.
func (s *WebSearchSupport) Accepts(search WebSearch, thinking ThinkingLevel) bool {
	switch {
	case s == nil || s.Tool == "":
		return false
	case s.RequiresThinking && (thinking == ThinkingLevelOff || thinking == ThinkingLevelUnknown):
		return false
	case len(search.AllowedDomains) > 0 && !s.AllowedDomains,
		len(search.ExcludedDomains) > 0 && !s.ExcludedDomains:
		return false
	case s.SingleDomainList && len(search.AllowedDomains) > 0 && len(search.ExcludedDomains) > 0:
		return false
	}
	return true
}

// WebSearch is the policy under which a run lets a model use provider-hosted
// search. Domain lists are bare hostnames, already normalized.
type WebSearch struct {
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	ExcludedDomains []string `json:"excluded_domains,omitempty"`
	// Replaces names the local tool hosted search stands in for. A request
	// that uses hosted search omits that tool; one that cannot keeps it.
	Replaces string `json:"replaces,omitempty"`
}

// Clone returns a copy that shares no slices.
func (s WebSearch) Clone() WebSearch {
	s.AllowedDomains = slices.Clone(s.AllowedDomains)
	s.ExcludedDomains = slices.Clone(s.ExcludedDomains)
	return s
}

// WebSearchAction is what one hosted search step did.
type WebSearchAction string

const (
	WebSearchActionSearch     WebSearchAction = "search"
	WebSearchActionOpenPage   WebSearchAction = "open_page"
	WebSearchActionFindInPage WebSearchAction = "find_in_page"
)

// WebSearchCall records one step of provider-hosted search inside an
// assistant message. The display fields are AICE's reading of the provider
// items; Replay holds those items unchanged so the same model can receive
// them back. No other model or protocol is sent Replay.
type WebSearchCall struct {
	ID      string            `json:"id"`
	Action  WebSearchAction   `json:"action,omitempty"`
	Queries []string          `json:"queries,omitempty"`
	URL     string            `json:"url,omitempty"`
	Sources []WebSearchSource `json:"sources,omitempty"`
	// Error is the provider's failure code when the step failed.
	Error  string            `json:"error,omitempty"`
	Replay []json.RawMessage `json:"replay"`
}

// WebSearchSource is one page a hosted search step reported.
type WebSearchSource struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// Clone returns a copy that shares no slices.
func (c WebSearchCall) Clone() WebSearchCall {
	c.Queries = slices.Clone(c.Queries)
	c.Sources = slices.Clone(c.Sources)
	replay := make([]json.RawMessage, len(c.Replay))
	for index, item := range c.Replay {
		replay[index] = slices.Clone(item)
	}
	c.Replay = replay
	return c
}

func (c WebSearchCall) validate() error {
	if c.ID == "" {
		return fmt.Errorf("web search id is required")
	}
	if len(c.Replay) == 0 {
		return fmt.Errorf("web search replay items are required")
	}
	for index, item := range c.Replay {
		if !json.Valid(item) {
			return fmt.Errorf("web search replay item %d is not valid json", index)
		}
	}
	return nil
}

// NewWebSearchPart wraps a hosted search step as assistant content.
func NewWebSearchPart(call WebSearchCall) ContentPart {
	return ContentPart{Type: ContentTypeWebSearch, WebSearch: &call}
}

// Describe is a one-line account of the step for compaction summaries and
// displays, such as `searched "go release"`.
func (c WebSearchCall) Describe() string {
	var description string
	switch c.Action {
	case WebSearchActionOpenPage:
		description = "opened " + c.URL
	case WebSearchActionFindInPage:
		description = "searched within " + c.URL
	default:
		quoted := make([]string, len(c.Queries))
		for index, query := range c.Queries {
			quoted[index] = strconv.Quote(query)
		}
		description = "searched " + strings.Join(quoted, ", ")
		if len(quoted) == 0 {
			description = "searched the web"
		}
	}
	if c.Error != "" {
		description += " (failed: " + c.Error + ")"
	}
	return description
}
