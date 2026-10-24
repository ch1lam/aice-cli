package browser

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestDefinitionRoundTripsChoices(t *testing.T) {
	for _, tc := range []struct {
		headed bool
		target Target
	}{
		{false, Target{}},
		{true, Target{Auto: true}},
		{false, Target{Endpoint: "9222"}},
		{true, Target{Endpoint: "ws://127.0.0.1:9222/devtools/browser/x"}},
	} {
		encoded, err := json.Marshal(Definition("/bin/agent-browser", tc.headed, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		settings, err := config.ParseMCPServerDefinition(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Args[0] != "mcp" || *settings.Env["AGENT_BROWSER_SESSION"].Value != Session {
			t.Fatalf("definition = %#v", settings)
		}
		if headed, target := Options(settings); headed != tc.headed || target != tc.target {
			t.Fatalf("options = %v %#v, want %v %#v", headed, target, tc.headed, tc.target)
		}
	}
}

func TestTargetValidation(t *testing.T) {
	for _, target := range []Target{{}, {Auto: true}, {Endpoint: "9222"}, {Endpoint: "wss://example.test/devtools"}} {
		if err := target.Validate(); err != nil {
			t.Fatalf("%#v: %v", target, err)
		}
	}
	for _, target := range []Target{{Endpoint: "0"}, {Endpoint: "http://localhost:9222"}, {Endpoint: "ws://user:pw@host/x"}, {Endpoint: "9222", Auto: true}} {
		if target.Validate() == nil {
			t.Fatalf("%#v accepted", target)
		}
	}
}
