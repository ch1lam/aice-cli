package guard

import (
	"context"
	"strings"
	"testing"
)

func TestNetworkRulesForWebTools(t *testing.T) {
	t.Parallel()
	g, err := New(t.TempDir(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// web_search without a bound service denies instead of asking.
	res, err := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"}))
	if err != nil || res.Decision != DecisionDeny || res.RuleID != RuleNetworkSearch {
		t.Fatalf("unbound web_search = %+v %v", res, err)
	}

	target := SearchTargetFingerprint("exa-main", "https://api.exa.ai")
	g.SetSearchTarget(target)
	res, err = g.Check(ctx, toolCall("web_search", map[string]any{"query": "go \x1b[31mcontext\x1b[0m " + strings.Repeat("长", 300)}))
	if err != nil || res.Decision != DecisionAsk || len(res.Approvals) != 1 {
		t.Fatalf("bound web_search = %+v %v", res, err)
	}
	approval := res.Approvals[0]
	if approval.RuleID != RuleNetworkSearch || approval.Action.Kind != "network" || approval.Action.Target != target || approval.Action.ToolName != "web_search" {
		t.Fatalf("approval = %+v", approval)
	}
	if strings.Contains(approval.Reason, "\x1b") || !strings.Contains(approval.Reason, "exa-main@https://api.exa.ai") || len(approval.Reason) > 400 {
		t.Fatalf("reason = %q", approval.Reason)
	}

	// A grant for the exact fingerprint allows; another instance or endpoint does not inherit it.
	g.AllowNetworkSession(target)
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAllow {
		t.Fatalf("granted search = %+v", res)
	}
	g.SetSearchTarget(SearchTargetFingerprint("exa-main", "http://127.0.0.1:9000"))
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAsk {
		t.Fatalf("endpoint change inherited grant: %+v", res)
	}
	g.SetSearchTarget(SearchTargetFingerprint("exa-eu", "https://api.exa.ai"))
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAsk {
		t.Fatalf("instance change inherited grant: %+v", res)
	}

	// web_fetch scopes by origin; invalid or blocked URL shapes deny.
	res, err = g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "HTTPS://Docs.Example.com:443/a/b?c=1"}))
	if err != nil || res.Decision != DecisionAsk || res.Approvals[0].Action.Target != FetchTargetScope("https://docs.example.com") || res.Approvals[0].RuleID != RuleNetworkFetch {
		t.Fatalf("fetch ask = %+v %v", res, err)
	}
	if !strings.Contains(res.Approvals[0].Reason, "https://docs.example.com/a/b?c=1") {
		t.Fatalf("fetch reason = %q", res.Approvals[0].Reason)
	}
	g.AllowNetworkSession(FetchTargetScope("https://docs.example.com"))
	if res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "https://docs.example.com/other"})); res.Decision != DecisionAllow {
		t.Fatalf("same origin not covered: %+v", res)
	}
	for _, url := range []string{"https://example.com/", "http://docs.example.com/", "https://docs.example.com:8443/"} {
		res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": url}))
		if res.Decision == DecisionAllow {
			t.Fatalf("%s covered by another origin's grant", url)
		}
	}
	for _, url := range []string{"", "ftp://example.com/", "https://u:p@example.com/", "https://example.com:8443/", "http://[fe80::1%25en0]/"} {
		res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": url}))
		if res.Decision != DecisionDeny || res.RuleID != RuleNetworkFetch {
			t.Fatalf("%q = %+v, want deny", url, res)
		}
	}
	// The grant does not leak into file or tool grants and clears with the Session.
	if res, _ := g.Check(ctx, toolCall("mystery_tool", map[string]any{})); res.Decision != DecisionAsk {
		t.Fatalf("unknown tool = %+v", res)
	}
	g.SetSearchTarget(target)
	g.ResetSessionGrants()
	if res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "https://docs.example.com/"})); res.Decision != DecisionAsk {
		t.Fatalf("fetch grant survived reset: %+v", res)
	}
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAsk {
		t.Fatalf("search grant survived reset: %+v", res)
	}
}
