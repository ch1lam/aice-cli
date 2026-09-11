package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func browserTestSession(t *testing.T) (*interactiveSession, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("browser unsupported on Windows")
	}
	dir, err := os.MkdirTemp("", "ab-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "bin")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$AGENT_BROWSER_SOCKET_DIR/commands"
case "$*" in
 *'close --json'*) rm -f "$AGENT_BROWSER_SOCKET_DIR/$2.pid" "$AGENT_BROWSER_SOCKET_DIR/$2.sock"; printf '%s\n' '{"success":true,"data":{"closed":true}}';;
 *'session info --json'*) printf '%s\n' '{"success":true,"data":{"active":true,"runtime":{"browserLaunched":true,"pageCount":1}}}';;
 *) printf '%s' '1' > "$AGENT_BROWSER_SOCKET_DIR/$2.pid"; printf '%s\n' '{"success":true,"data":{"tabs":[{"tabId":"t1","targetId":"ABC123","title":"Example","url":"https://example.com","active":true}]}}';;
esac
`
	for path, data := range map[string]string{filepath.Join(bin, "agent-browser"): script, filepath.Join(bin, "agent-browser.version"): deps.AgentBrowserVersion, filepath.Join(bin, "agent-browser-skills", deps.AgentBrowserVersion, "core", "SKILL.md"): "core"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := browser.NewManager(os.Getpid(), bin, dir)
	if err != nil {
		t.Fatal(err)
	}
	for key := range m.Environment() {
		t.Setenv(key, os.Getenv(key))
	}
	if err := applyBrowserEnvironment(m); err != nil {
		t.Fatal(err)
	}
	g, err := guard.New(dir, guard.Config{})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &interactiveSession{browser: m, guard: g, workspace: workspace, workspacePath: dir}, dir
}
func TestBrowserCommandsAndSessionRotation(t *testing.T) {
	s, dir := browserTestSession(t)
	command := interactiveSlashCommand(t, s.SlashCommands(), "browser")
	if command.Menu == nil || len(command.Menu.Options) != 5 || !command.Interactive {
		t.Fatal("browser menu incomplete")
	}
	input := make(chan string, 1)
	ui := &interaction.AuthInteraction{Input: input, Notify: func(_ context.Context, p interaction.AuthPrompt) error {
		if p.Menu == nil {
			input <- "9222"
		} else {
			input <- "ABC123"
		}
		return nil
	}}
	if _, err := s.slashBrowser(t.Context(), interaction.CommandRequest{Arguments: "connect", Auth: ui}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AGENT_BROWSER_CDP") != "9222" {
		t.Fatal("CDP not inherited")
	}
	data, err := os.ReadFile(filepath.Join(s.browser.RunDir(), "commands"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--cdp 9222 --pin-tab tab list --json") || !strings.Contains(string(data), "tab ABC123 --json") {
		t.Fatalf("commands %s", data)
	}
	before := s.browser.Name()
	bash, err := tool.NewBash(s.workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := bash.Execute(t.Context(), llm.ToolCall{ID: "env", Name: "bash", Arguments: []byte(`{"command":"printenv AGENT_BROWSER_SESSION"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, before) {
		t.Fatalf("bash env %+v", result)
	}
	if _, err := s.slashNew(t.Context(), interaction.CommandRequest{}); err != nil {
		t.Fatal(err)
	}
	if s.browser.Name() == before || os.Getenv("AGENT_BROWSER_SESSION") != s.browser.Name() || os.Getenv("AGENT_BROWSER_CDP") != "" {
		t.Fatal("browser state not rotated")
	}
	if _, err := os.Stat(filepath.Join(dir, "browser", "run", before+".pid")); !os.IsNotExist(err) {
		t.Fatal("old daemon not closed")
	}
}
func TestBrowserCleanupIgnoresCancelledParent(t *testing.T) {
	s, _ := browserTestSession(t)
	sidecar := filepath.Join(s.browser.RunDir(), s.browser.Name()+".pid")
	if err := os.WriteFile(sidecar, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := closeBrowser(ctx, s.browser); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("cancelled parent prevented cleanup")
	}
}
