package guard

import "testing"

func TestRemovedDesktopNamesUseUnknownToolPolicy(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"desktop_apps", "desktop_observe", "desktop_act"} {
		g, err := New(t.TempDir(), Config{})
		if err != nil {
			t.Fatal(err)
		}
		call := toolCall(name, map[string]any{})
		checkAsk := func() {
			t.Helper()
			got, err := g.Check(t.Context(), call)
			if err != nil || got.Decision != DecisionAsk || len(got.Approvals) != 1 || got.Approvals[0].RuleID != "unknownTool" {
				t.Fatalf("%s retained legacy desktop authority: %+v, %v", name, got, err)
			}
		}
		checkAsk()
		g.AllowToolSession(name)
		if got, err := g.Check(t.Context(), call); err != nil || got.Decision != DecisionAllow {
			t.Fatalf("ordinary explicit tool grant failed: %+v, %v", got, err)
		}
		g.ResetSessionGrants()
		checkAsk()
	}
}
