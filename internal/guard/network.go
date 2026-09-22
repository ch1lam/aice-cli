package guard

import (
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Network rule identifiers for the two built-in web tools.
const (
	RuleNetworkSearch = "network.search"
	RuleNetworkFetch  = "network.fetch"
)

// SearchTargetFingerprint names the bound search service: instance ID plus
// endpoint origin, so the guard can tell whether a search backend is bound
// and which identity it carries.
func SearchTargetFingerprint(instanceID, endpointOrigin string) string {
	return "search:" + instanceID + "@" + strings.ToLower(endpointOrigin)
}

// SetSearchTarget records the fingerprint of the search service bound for the
// next runs. An empty value means no service is bound and web_search denies.
func (g *Guard) SetSearchTarget(fingerprint string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.searchTarget = fingerprint
}

// checkNetwork decides web_search and web_fetch calls. Enabled web tools run
// without a per-call, per-origin or per-Session confirmation: a bound search
// service allows web_search, and a URL that passes the shared shape check
// allows web_fetch. Explicitly rejected shapes still deny. It performs no DNS
// or connection: address policy runs inside the tool, outside the guard lock.
// The caller holds g.mu for reading.
func (g *Guard) checkNetwork(call llm.ToolCall) Result {
	switch call.Name {
	case "web_search":
		if g.searchTarget == "" {
			return Result{Decision: DecisionDeny, Reason: "web_search has no bound search service", RuleID: RuleNetworkSearch, Action: Action{Kind: "network", ToolName: call.Name}}
		}
		return Result{Decision: DecisionAllow, Action: Action{Kind: "network", ToolName: call.Name, Target: g.searchTarget}}
	case "web_fetch":
		raw := extractStringArgument(call.Arguments, "url")
		target, err := web.ValidateFetchURL(raw)
		if err != nil {
			return Result{Decision: DecisionDeny, Reason: "web_fetch url rejected: " + err.Error(), RuleID: RuleNetworkFetch, Action: Action{Kind: "network", ToolName: call.Name}}
		}
		return Result{Decision: DecisionAllow, Action: Action{Kind: "network", ToolName: call.Name, Target: target.Origin}}
	}
	return Result{Decision: DecisionAllow}
}
