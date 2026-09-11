//go:build integration && !windows

package browser

import (
	"context"
	"encoding/json"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run with AICE_BROWSER_TEST_HELPER pointing to the verified pinned binary.
// This opt-in check uses only a local data URL and a separate headless profile.
func TestNativeManagedBrowserLifecycle(t *testing.T) {
	helper := os.Getenv("AICE_BROWSER_TEST_HELPER")
	if helper == "" {
		t.Skip("set AICE_BROWSER_TEST_HELPER to the pinned native helper")
	}
	data, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t)
	m.pid = os.Getpid()
	if err := os.WriteFile(filepath.Join(m.binDir, "agent-browser"), data, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	t.Cleanup(func() {
		if err := m.Close(context.WithoutCancel(ctx)); err != nil {
			t.Error(err)
		}
	})
	page := `<title>AICE browser fixture</title><form><label>Name<input name="name"></label><button type="button" onclick="document.title='Submitted'">Submit</button></form>`
	run := func(args ...string) []byte {
		t.Helper()
		out, err := m.command(ctx, m.Name(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeResult(out, nil); err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("open", "data:text/html,"+url.PathEscape(page), "--json")
	snapshot := run("snapshot", "-i", "--json")
	if !strings.Contains(string(snapshot), "Submit") {
		t.Fatalf("snapshot %s", snapshot)
	}
	run("fill", "input[name=name]", "AICE", "--json")
	run("click", "button", "--json")
	title := run("get", "title", "--json")
	if !strings.Contains(string(title), "Submitted") {
		t.Fatal(string(title))
	}
	shot := filepath.Join(m.ScreenshotDir(), "page.png")
	run("screenshot", shot, "--json")
	file, err := os.Open(shot)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(file)
	file.Close()
	if err != nil || config.Width == 0 {
		t.Fatalf("screenshot %v %+v", err, config)
	}
	info, err := m.Info(ctx)
	if err != nil || !info.Active || !info.Runtime.BrowserLaunched {
		t.Fatalf("info %+v: %v", info, err)
	}
	var response map[string]any
	if err := json.Unmarshal(info.Raw, &response); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if m.HasSidecar() {
		t.Fatal("close returned before daemon cleanup")
	}
	if err := m.Rotate(); err != nil {
		t.Fatal(err)
	}
	run("open", "about:blank", "--json")
}
