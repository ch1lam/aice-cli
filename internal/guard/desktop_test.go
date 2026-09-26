package guard

import (
	"testing"
)

func TestDesktopToggleIsIndependentOfGenericGrants(t *testing.T) {
	t.Parallel()
	for _, guardEnabled := range []bool{true, false} {
		g, err := New(t.TempDir(), Config{Enabled: &guardEnabled})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"desktop_apps", "desktop_observe", "desktop_act"} {
			g.AllowToolSession(name)
			for _, enabled := range []bool{false, true, false} {
				g.SetDesktopEnabled(enabled)
				got, err := g.Check(t.Context(), toolCall(name, map[string]any{}))
				want := DecisionDeny
				if enabled {
					want = DecisionAllow
				}
				if err != nil || got.Decision != want {
					t.Fatalf("guard=%v desktop=%v result=%+v err=%v", guardEnabled, enabled, got, err)
				}
			}
		}
	}
}
