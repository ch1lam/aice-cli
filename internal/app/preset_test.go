package app

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
)

// A preset rewrite keeps the user's own fields and env entries, and drops
// owned env keys that the new choice no longer sets.
func TestPresetMergeKeepsUserFields(t *testing.T) {
	path, cdp := "/Applications/Chromium.app/Contents/MacOS/Chromium", "9222"
	include := []string{"agent_browser_open"}
	existing := browser.Definition("/old/agent-browser", false, browser.Target{Endpoint: cdp})
	existing.IncludeTools = &include
	existing.CallTimeout = "30s"
	existing.Env["AGENT_BROWSER_EXECUTABLE_PATH"] = config.MCPValueRef{Value: &path}
	merged := presetMerge(existing, browser.Definition("/new/agent-browser", true, browser.Target{}), browser.OwnedEnv)
	if merged.Command != "/new/agent-browser" || merged.CallTimeout != "30s" || !reflect.DeepEqual(merged.IncludeTools, &include) {
		t.Fatalf("merged = %#v", merged)
	}
	if ref := merged.Env["AGENT_BROWSER_EXECUTABLE_PATH"]; ref.Value == nil || *ref.Value != path {
		t.Fatal("user env entry was dropped")
	}
	if _, kept := merged.Env["AGENT_BROWSER_CDP"]; kept {
		t.Fatal("cleared target survived the rewrite")
	}
	if headed, target := browser.Options(merged); !headed || target != (browser.Target{}) {
		t.Fatalf("choices = %v %#v", headed, target)
	}
}
