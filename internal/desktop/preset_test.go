package desktop

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestDefinitionIsAnOrdinaryMCPServer(t *testing.T) {
	encoded, err := json.Marshal(Definition("/Applications/CuaDriver.app/Contents/MacOS/cua-driver"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.ParseMCPServerDefinition(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Transport != "stdio" || len(settings.Args) != 1 || settings.Args[0] != "mcp" || settings.Cwd != "" {
		t.Fatalf("definition = %#v", settings)
	}
	for _, name := range []string{"CUA_DRIVER_RS_TELEMETRY_ENABLED", "CUA_DRIVER_RS_UPDATE_CHECK"} {
		if value := settings.Env[name].Value; value == nil || *value != "false" {
			t.Fatalf("%s = %v", name, value)
		}
	}
}
