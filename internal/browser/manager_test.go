package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	// Darwin's socket address is limited to 103 bytes; testing.TempDir includes
	// long test names, so use a short isolated path for the real constructor.
	dir, err := os.MkdirTemp("", "ab-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "bin")
	for path, data := range map[string]string{
		filepath.Join(bin, "agent-browser"):                                                      "fake",
		filepath.Join(bin, "agent-browser.version"):                                              deps.AgentBrowserVersion,
		filepath.Join(bin, "agent-browser-skills", deps.AgentBrowserVersion, "core", "SKILL.md"): "core",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := NewManager(123, bin, dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManagerEnvironment(t *testing.T) {
	m := testManager(t)
	if m.Name() != "aice-123-1" {
		t.Fatal(m.Name())
	}
	env := m.Environment()
	if env["AGENT_BROWSER_SESSION"] != m.Name() || env["AGENT_BROWSER_SOCKET_DIR"] != m.RunDir() || env["AGENT_BROWSER_SCREENSHOT_DIR"] != m.ScreenshotDir() {
		t.Fatal(env)
	}
	if err := m.Rotate(); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "aice-123-2" {
		t.Fatal(m.Name())
	}
	if _, err := NewManager(1, filepath.Join(t.TempDir(), strings.Repeat("x", 105), "bin"), t.TempDir()); err == nil {
		t.Fatal("accepted oversized socket")
	}
}
func TestManagerConnectAndBind(t *testing.T) {
	for _, target := range []Target{{Auto: true}, {Endpoint: "9222"}, {Endpoint: "ws://localhost:9222/devtools/browser/123"}} {
		t.Run(fmt.Sprintf("%s/auto=%v", target.Endpoint, target.Auto), func(t *testing.T) {
			m := testManager(t)
			var got []string
			m.exec = func(_ context.Context, args, env []string) ([]byte, error) {
				got = args
				return []byte(`{"success":true,"data":{"tabs":[{"tabId":"t1","targetId":"ABC123","title":"Example","url":"https://example.com","active":true}]}}`), nil
			}
			tabs, err := m.Connect(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"--session", m.Name(), "--cdp", target.Endpoint, "--pin-tab", "tab", "list", "--json"}
			if target.Auto {
				expected = []string{"--session", m.Name(), "--auto-connect", "--pin-tab", "tab", "list", "--json"}
			}
			if !reflect.DeepEqual(got, expected) || len(tabs) != 1 || tabs[0].TargetID != "ABC123" {
				t.Fatalf("args %v tabs %+v", got, tabs)
			}
			if m.Environment()["AGENT_BROWSER_CDP"] != target.Endpoint {
				t.Fatal("lost CDP target")
			}
			if err := m.BindTab(t.Context(), tabs[0].TargetID); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, []string{"--session", m.Name(), "tab", "ABC123", "--json"}) {
				t.Fatal(got)
			}
		})
	}
}
func TestManagerRejectsInvalidTarget(t *testing.T) {
	for _, target := range []Target{{Endpoint: "9222; rm"}, {Endpoint: "0"}, {Endpoint: "65536"}, {Endpoint: "https://example.com"}, {Auto: true, Endpoint: "9222"}, {}} {
		t.Run(target.Endpoint, func(t *testing.T) {
			m := testManager(t)
			m.exec = func(context.Context, []string, []string) ([]byte, error) {
				t.Fatal("executed invalid target")
				return nil, nil
			}
			if _, err := m.Connect(t.Context(), target); err == nil {
				t.Fatal("accepted invalid target")
			}
		})
	}
}
func TestManagerCloseAndMissingHelper(t *testing.T) {
	m := testManager(t)
	calls := 0
	m.exec = func(_ context.Context, args, env []string) ([]byte, error) {
		calls++
		for _, v := range env {
			if strings.HasPrefix(v, "AGENT_BROWSER_CDP=") {
				t.Fatal("close reconnects")
			}
		}
		return []byte(`{"success":true,"data":{"closed":true}}`), nil
	}
	if err := m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("close started daemon")
	}
	if err := os.WriteFile(filepath.Join(m.runDir, m.Name()+".pid"), []byte("321"), 0600); err != nil {
		t.Fatal(err)
	}
	m.target = Target{Endpoint: "9222"}
	if err := m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || m.target != (Target{}) {
		t.Fatal("close did not clear connection")
	}
	m.closeTimeout = time.Millisecond
	m.exec = func(ctx context.Context, _, _ []string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	if err := m.Close(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
	if err := os.Remove(filepath.Join(m.binDir, "agent-browser")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Executable(); !errors.Is(err, ErrHelperMissing) {
		t.Fatal(err)
	}
}
func TestManagerSweepOnlyDeadOwners(t *testing.T) {
	m := testManager(t)
	m.alive = func(pid int) bool { return pid == 456 }
	for _, name := range []string{m.Name() + ".pid", "aice-456-1.pid", "aice-789-2.pid", "someone-999.pid"} {
		if err := os.WriteFile(filepath.Join(m.runDir, name), []byte("1"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	m.exec = func(_ context.Context, args, _ []string) ([]byte, error) {
		names = append(names, args[1])
		return []byte(`{"success":true,"data":{"closed":true}}`), nil
	}
	if errs := m.SweepStale(t.Context()); len(errs) != 0 {
		t.Fatal(errs)
	}
	if !reflect.DeepEqual(names, []string{"aice-789-2"}) {
		t.Fatal(names)
	}
}
func TestManagerRejectsFailedAndMalformedJSON(t *testing.T) {
	for _, response := range []string{`not json`, `{"success":false,"error":"tab_gone"}`, `{"success":true,"data":"wrong"}`} {
		t.Run(response, func(t *testing.T) {
			if _, err := parseTabs([]byte(response)); err == nil {
				t.Fatal("accepted response")
			}
		})
	}
}
