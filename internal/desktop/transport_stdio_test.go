package desktop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Run the desktop admission path over real generic-client pipes. The helper
// contains only synthetic Driver metadata and never contacts a desktop service.
func TestDesktopStdioHelper(t *testing.T) {
	if os.Getenv("AICE_DESKTOP_TEST_HELPER") != "1" {
		return
	}
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("COMSPEC") != "" || os.Getenv("CUA_DRIVER_PERMISSION_MODE") != "standard" {
		os.Exit(3)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(4)
		}
		if len(request.ID) == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			if !bytes.Contains(request.Params, []byte(ProtocolVersion)) {
				os.Exit(5)
			}
			result = map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]string{"name": "cua-driver", "version": DriverVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			list := []any{}
			for name, schema := range schemaFixture(t) {
				list = append(list, map[string]any{"name": name, "inputSchema": schema})
			}
			result = map[string]any{"tools": list}
		case "tools/call":
			result = json.RawMessage(`{"structuredContent":{"n":9007199254740993},"isError":true,"content":[{"type":"text","text":"partial"},{"type":"image","mimeType":"image/png","data":"AQID"}]}`)
		default:
			os.Exit(6)
		}
		response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if err != nil {
			os.Exit(7)
		}
		fmt.Fprintln(os.Stdout, string(response))
	}
	os.Exit(0)
}

func TestDesktopUsesGenericStdioAdmissionAndExactResults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-private")
	t.Setenv("COMSPEC", "synthetic-forbidden")
	t.Setenv("CUA_DRIVER_PERMISSION_MODE", "unrestricted")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := driverMCPConfig(binary, t.TempDir(), "-test.run=^TestDesktopStdioHelper$")
	config.Stdio.Env["AICE_DESKTOP_TEST_HELPER"] = "1"
	config.Stdio.Env["GORACE"] = "atexit_sleep_ms=0"
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.close() })
	if c.connection.StdioPID() == 0 || c.connection.Info().ProtocolVersion != ProtocolVersion {
		t.Fatal("desktop did not use pinned generic stdio connection")
	}
	reply, err := c.call(t.Context(), "click", map[string]any{"pid": 1})
	if err != nil || !reply.IsError || !strings.Contains(string(reply.Structured), "9007199254740993") || len(reply.Images) != 1 || len(reply.Text) != 1 {
		t.Fatal("desktop lost exact or partial result", err)
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
}
