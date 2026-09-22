package guard

import (
	"context"
	"testing"
)

func TestNetworkRulesForWebTools(t *testing.T) {
	t.Parallel()
	g, err := New(t.TempDir(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// web_search without a bound service denies; it never asks.
	res, err := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"}))
	if err != nil || res.Decision != DecisionDeny || res.RuleID != RuleNetworkSearch {
		t.Fatalf("unbound web_search = %+v %v", res, err)
	}
	if len(res.Approvals) != 0 {
		t.Fatalf("unbound web_search approvals = %+v", res.Approvals)
	}

	// A bound search service allows without confirmation and without a
	// session grant. Control characters in the query never reach a prompt
	// because no approval is produced.
	target := SearchTargetFingerprint("exa-main", "https://api.exa.ai")
	g.SetSearchTarget(target)
	res, err = g.Check(ctx, toolCall("web_search", map[string]any{"query": "go context"}))
	if err != nil || res.Decision != DecisionAllow || len(res.Approvals) != 0 {
		t.Fatalf("bound web_search = %+v %v", res, err)
	}
	if res.Action.Kind != "network" || res.Action.Target != target || res.Action.ToolName != "web_search" {
		t.Fatalf("action = %+v", res.Action)
	}

	// Rebinding to another instance or endpoint still allows directly; there
	// is no per-scope grant to inherit or refresh.
	g.SetSearchTarget(SearchTargetFingerprint("exa-main", "http://127.0.0.1:9000"))
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAllow {
		t.Fatalf("rebound endpoint = %+v", res)
	}
	g.SetSearchTarget(SearchTargetFingerprint("exa-eu", "https://api.exa.ai"))
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAllow {
		t.Fatalf("rebound instance = %+v", res)
	}
	g.SetSearchTarget(target)

	// web_fetch allows any URL that passes the shared shape check, across
	// origins and sessions, without approvals.
	for _, url := range []string{
		"HTTPS://Docs.Example.com:443/a/b?c=1",
		"https://docs.example.com/other",
		"https://example.com/",
		"http://docs.example.com/",
		"https://other.example/b",
	} {
		res, err := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": url}))
		if err != nil || res.Decision != DecisionAllow || len(res.Approvals) != 0 {
			t.Fatalf("fetch %q = %+v %v", url, res, err)
		}
		if res.RuleID != "" {
			t.Fatalf("fetch %q rule = %q, want empty on allow", url, res.RuleID)
		}
	}
	res, err = g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "https://docs.example.com/other"}))
	if err != nil || res.Decision != DecisionAllow || res.Action.Kind != "network" || res.Action.ToolName != "web_fetch" || res.Action.Target != "https://docs.example.com" {
		t.Fatalf("fetch action = %+v %v", res, err)
	}

	// Invalid URL shapes deny without a network request and without asking.
	for _, url := range []string{"", "ftp://example.com/", "https://u:p@example.com/", "https://example.com:8443/", "http://[fe80::1%25en0]/"} {
		res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": url}))
		if res.Decision != DecisionDeny || res.RuleID != RuleNetworkFetch || len(res.Approvals) != 0 {
			t.Fatalf("%q = %+v, want deny without approvals", url, res)
		}
	}

	// Resetting session grants keeps the web default: legitimate calls still
	// allow, and unrelated gates are untouched.
	g.ResetSessionGrants()
	if res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "https://docs.example.com/"})); res.Decision != DecisionAllow {
		t.Fatalf("fetch after reset = %+v", res)
	}
	if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "x"})); res.Decision != DecisionAllow {
		t.Fatalf("search after reset = %+v", res)
	}
	if res, _ := g.Check(ctx, toolCall("mystery_tool", map[string]any{})); res.Decision != DecisionAsk {
		t.Fatalf("unknown tool = %+v", res)
	}
}

func TestNetworkDefaultsSurviveNewSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := SearchTargetFingerprint("exa-main", "https://api.exa.ai")
	for i := 0; i < 2; i++ {
		g, err := New(t.TempDir(), Config{})
		if err != nil {
			t.Fatal(err)
		}
		g.SetSearchTarget(target)
		if res, _ := g.Check(ctx, toolCall("web_search", map[string]any{"query": "fresh session"})); res.Decision != DecisionAllow {
			t.Fatalf("session %d search = %+v", i, res)
		}
		if res, _ := g.Check(ctx, toolCall("web_fetch", map[string]any{"url": "https://fresh.example/page"})); res.Decision != DecisionAllow {
			t.Fatalf("session %d fetch = %+v", i, res)
		}
	}
}
