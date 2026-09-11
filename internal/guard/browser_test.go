package guard

import "testing"

func TestBrowserCommandsKeepExistingPathBoundary(t *testing.T) {
	g, err := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, command string
		want          Decision
	}{
		{"navigate", "agent-browser open https://a.b/c", DecisionAllow},
		{"click", "agent-browser click @e3", DecisionAllow},
		{"fill", `agent-browser fill @e2 "a.b"`, DecisionAllow},
		{"screenshot", "agent-browser screenshot page.png", DecisionAllow},
		{"workspace screenshot", "agent-browser screenshot .aice/browser/screenshots/page.png", DecisionAllow},
		{"external screenshot", "agent-browser screenshot /tmp/x.png", DecisionAsk},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := g.Check(t.Context(), toolCall("bash", map[string]any{"command": test.command}))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.want {
				t.Fatalf("decision %+v, want %v", result, test.want)
			}
		})
	}
}
