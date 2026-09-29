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
			m.exec = func(_ context.Context, args, env []string) ([]byte, bool, error) {
				got = args
				return []byte(`{"success":true,"data":{"tabs":[{"tabId":"t1","targetId":"ABC123","title":"Example","url":"https://example.com","active":true}]}}`), true, nil
			}
			tabs, changed, err := m.Connect(t.Context(), target)
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
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
			if changed, err := m.BindTab(t.Context(), tabs[0].TargetID); err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
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
			m.exec = func(context.Context, []string, []string) ([]byte, bool, error) {
				t.Fatal("executed invalid target")
				return nil, false, nil
			}
			if _, changed, err := m.Connect(t.Context(), target); err == nil || changed {
				t.Fatal("accepted invalid target")
			}
		})
	}
}
func TestManagerCloseAndMissingHelper(t *testing.T) {
	m := testManager(t)
	calls := 0
	m.exec = func(_ context.Context, args, env []string) ([]byte, bool, error) {
		calls++
		os.Remove(filepath.Join(m.runDir, m.Name()+".pid"))
		for _, v := range env {
			if strings.HasPrefix(v, "AGENT_BROWSER_CDP=") {
				t.Fatal("close reconnects")
			}
		}
		return []byte(`{"success":true,"data":{"closed":true}}`), true, nil
	}
	if changed, err := m.Close(t.Context()); err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if calls != 0 {
		t.Fatal("close started daemon")
	}
	if err := os.WriteFile(filepath.Join(m.runDir, m.Name()+".pid"), []byte("321"), 0600); err != nil {
		t.Fatal(err)
	}
	m.target = Target{Endpoint: "9222"}
	if changed, err := m.Close(t.Context()); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if calls != 1 || m.target != (Target{}) {
		t.Fatal("close did not clear connection")
	}
	if err := os.WriteFile(filepath.Join(m.runDir, m.Name()+".pid"), []byte("321"), 0600); err != nil {
		t.Fatal(err)
	}
	m.closeTimeout = time.Millisecond
	m.exec = func(ctx context.Context, _, _ []string) ([]byte, bool, error) {
		<-ctx.Done()
		return nil, true, ctx.Err()
	}
	if changed, err := m.Close(t.Context()); !errors.Is(err, context.DeadlineExceeded) || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
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
	m.exec = func(_ context.Context, args, _ []string) ([]byte, bool, error) {
		names = append(names, args[1])
		os.Remove(filepath.Join(m.runDir, args[1]+".pid"))
		return []byte(`{"success":true,"data":{"closed":true}}`), true, nil
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

func TestManagerMutationBeforeDispatch(t *testing.T) {
	for _, action := range []string{"connect", "bind", "new"} {
		t.Run(action, func(t *testing.T) {
			m := testManager(t)
			m.target = Target{Endpoint: "9222"}
			m.exec = func(context.Context, []string, []string) ([]byte, bool, error) {
				t.Fatal("dispatched cancelled operation")
				return nil, false, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			var changed bool
			var err error
			switch action {
			case "connect":
				_, changed, err = m.Connect(ctx, Target{Endpoint: "9333"})
			case "bind":
				changed, err = m.BindTab(ctx, "ABC")
			case "new":
				changed, err = m.NewTab(ctx)
			}
			if changed || !errors.Is(err, context.Canceled) || m.Target().Endpoint != "9222" || m.Name() != "aice-123-1" {
				t.Fatalf("changed=%v err=%v target=%+v name=%s", changed, err, m.Target(), m.Name())
			}
		})
	}
	t.Run("invalid tab", func(t *testing.T) {
		m := testManager(t)
		m.exec = func(context.Context, []string, []string) ([]byte, bool, error) {
			t.Fatal("dispatched invalid tab")
			return nil, false, nil
		}
		if changed, err := m.BindTab(t.Context(), "bad tab"); changed || err == nil {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
	})
}

func TestManagerTabMutationFacts(t *testing.T) {
	failure := errors.New("helper failed")
	for _, action := range []string{"bind", "new"} {
		for _, tc := range []struct {
			name    string
			started bool
			data    string
			err     error
		}{
			{name: "not started", err: failure},
			{name: "started error", started: true, err: failure},
			{name: "started cancelled", started: true, err: context.Canceled},
			{name: "malformed response", started: true, data: "not json"},
			{name: "rejected response", started: true, data: `{"success":false,"error":"tab_gone"}`},
			{name: "success", started: true, data: `{"success":true}`},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				m := testManager(t)
				calls := 0
				m.exec = func(context.Context, []string, []string) ([]byte, bool, error) {
					calls++
					return []byte(tc.data), tc.started, tc.err
				}
				var changed bool
				var err error
				if action == "bind" {
					changed, err = m.BindTab(t.Context(), "ABC")
				} else {
					changed, err = m.NewTab(t.Context())
				}
				if changed != tc.started || calls != 1 || (err == nil) != (tc.name == "success") {
					t.Fatalf("changed=%v calls=%d err=%v", changed, calls, err)
				}
				if tc.err != nil && !errors.Is(err, tc.err) {
					t.Fatalf("lost error: %v", err)
				}
			})
		}
	}
}

func TestManagerConnectPartialEffects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		started bool
		data    string
		err     error
	}{
		{name: "not started", err: errors.New("start failed")},
		{name: "started error", started: true, err: errors.New("helper failed")},
		{name: "malformed response", started: true, data: "not json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t)
			m.target = Target{Endpoint: "9222"}
			m.exec = func(context.Context, []string, []string) ([]byte, bool, error) {
				return []byte(tc.data), tc.started, tc.err
			}
			_, changed, err := m.Connect(t.Context(), Target{Endpoint: "9333"})
			if !changed || err == nil || m.Target().Endpoint != "9333" || m.Name() != "aice-123-2" {
				t.Fatalf("changed=%v err=%v target=%+v name=%s", changed, err, m.Target(), m.Name())
			}
		})
	}
}

func TestManagerCloseFailureFacts(t *testing.T) {
	for _, action := range []string{"close", "connect"} {
		for _, hasTarget := range []bool{false, true} {
			for _, started := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/target=%v/started=%v", action, hasTarget, started), func(t *testing.T) {
					m := testManager(t)
					if hasTarget {
						m.target = Target{Endpoint: "9222"}
					}
					if err := os.WriteFile(filepath.Join(m.runDir, m.Name()+".pid"), []byte("321"), 0600); err != nil {
						t.Fatal(err)
					}
					failure := errors.New("close failed")
					calls := 0
					m.exec = func(_ context.Context, args, _ []string) ([]byte, bool, error) {
						calls++
						if args[2] != "close" {
							t.Fatalf("continued connection after close failure: %v", args)
						}
						return nil, started, failure
					}
					var changed bool
					var err error
					if action == "close" {
						changed, err = m.Close(t.Context())
					} else {
						_, changed, err = m.Connect(t.Context(), Target{Endpoint: "9333"})
					}
					if changed != (hasTarget || started) || !errors.Is(err, failure) || calls != 1 || m.Target() != (Target{}) || m.Name() != "aice-123-1" {
						t.Fatalf("changed=%v err=%v calls=%d target=%+v name=%s", changed, err, calls, m.Target(), m.Name())
					}
				})
			}
		}
	}
}
