package guard

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Network rule identifiers for the two built-in web tools.
const (
	RuleNetworkSearch = "network.search"
	RuleNetworkFetch  = "network.fetch"
)

const maxReasonQueryBytes = 200

// SearchTargetFingerprint names the bound search service for grants and
// prompts: instance ID plus endpoint origin, so a changed endpoint or a
// different instance never inherits an earlier approval.
func SearchTargetFingerprint(instanceID, endpointOrigin string) string {
	return "search:" + instanceID + "@" + strings.ToLower(endpointOrigin)
}

// FetchTargetScope names one fetch origin for grants and prompts.
func FetchTargetScope(origin string) string {
	return "fetch:" + strings.ToLower(origin)
}

// SetSearchTarget records the fingerprint of the search service bound for the
// next runs. An empty value means no service is bound and web_search denies.
// Replacing the target does not clear grants; a new fingerprint simply never
// matches an old grant.
func (g *Guard) SetSearchTarget(fingerprint string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.searchTarget = fingerprint
}

// AllowNetworkSession records a network scope grant for the remainder of this
// Session. Scopes are the exact strings produced by SearchTargetFingerprint
// and FetchTargetScope.
func (g *Guard) AllowNetworkSession(scope string) {
	if g == nil || scope == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionNetwork == nil {
		g.sessionNetwork = make(map[string]bool)
	}
	g.sessionNetwork[scope] = true
}

// checkNetwork decides web_search and web_fetch calls. It performs no DNS or
// connection: address policy runs inside the tool, outside the guard lock.
// The caller holds g.mu for reading.
func (g *Guard) checkNetwork(call llm.ToolCall) Result {
	switch call.Name {
	case "web_search":
		if g.searchTarget == "" {
			return Result{Decision: DecisionDeny, Reason: "web_search has no bound search service", RuleID: RuleNetworkSearch, Action: Action{Kind: "network", ToolName: call.Name}}
		}
		action := Action{Kind: "network", ToolName: call.Name, Target: g.searchTarget}
		if g.sessionNetwork[g.searchTarget] {
			return Result{Decision: DecisionAllow, Action: action}
		}
		query := displayQuery(extractStringArgument(call.Arguments, "query"))
		return Result{Decision: DecisionAsk, Approvals: []Approval{{
			Reason: fmt.Sprintf("web_search requires confirmation: it will send the query %q to %s.", query, strings.TrimPrefix(g.searchTarget, "search:")),
			RuleID: RuleNetworkSearch,
			Action: action,
		}}}
	case "web_fetch":
		raw := extractStringArgument(call.Arguments, "url")
		target, err := web.ValidateFetchURL(raw)
		if err != nil {
			return Result{Decision: DecisionDeny, Reason: "web_fetch url rejected: " + err.Error(), RuleID: RuleNetworkFetch, Action: Action{Kind: "network", ToolName: call.Name}}
		}
		scope := FetchTargetScope(target.Origin)
		action := Action{Kind: "network", ToolName: call.Name, Target: scope}
		if g.sessionNetwork[scope] {
			return Result{Decision: DecisionAllow, Action: action}
		}
		return Result{Decision: DecisionAsk, Approvals: []Approval{{
			Reason: fmt.Sprintf("web_fetch requires confirmation: it will request %s (origin %s).", web.Sanitize(target.URL), target.Origin),
			RuleID: RuleNetworkFetch,
			Action: action,
		}}}
	}
	return Result{Decision: DecisionAllow}
}

// displayQuery bounds and sanitizes model-controlled text before it appears
// in a permission prompt.
func displayQuery(query string) string {
	query = strings.Join(strings.Fields(web.Sanitize(query)), " ")
	if len(query) <= maxReasonQueryBytes {
		return query
	}
	cut := maxReasonQueryBytes
	for cut > 0 && !utf8.RuneStart(query[cut]) {
		cut--
	}
	return query[:cut] + "…"
}
