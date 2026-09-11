// Package browser owns process-scoped agent-browser sessions, not transcript state.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
)

var ErrHelperMissing = errors.New("pinned browser helper is unavailable; restart AICE or check network and AICE_NO_DEP_INSTALL")

// Target is either automatic discovery or an explicit CDP port/WebSocket URL.
type Target struct {
	Auto     bool
	Endpoint string
}

// Tab contains upstream's stable tab reference and CDP target identity.
type Tab struct {
	ID       string `json:"tabId"`
	TargetID string `json:"targetId"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Active   bool   `json:"active"`
}

// Info contains read-only daemon status. Raw retains upstream's runtime detail.
type Info struct {
	Active  bool `json:"active"`
	PID     int  `json:"pid"`
	Runtime struct {
		BrowserLaunched bool `json:"browserLaunched"`
		PageCount       int  `json:"pageCount"`
	} `json:"runtime"`
	Raw json.RawMessage `json:"-"`
}

// Manager is owned by one application's idle command/exit lifecycle. Calls are
// serialized by that owner; the Agent Loop only inherits Environment values.
type Manager struct {
	pid, gen                  int
	binDir, runDir, workspace string
	target                    Target
	exec                      func(context.Context, []string, []string) ([]byte, error)
	closeTimeout              time.Duration
	alive                     func(int) bool
}

func NewManager(pid int, binDir, workspace string) (*Manager, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid browser owner pid")
	}
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	m := &Manager{pid: pid, gen: 1, binDir: binDir, runDir: filepath.Join(filepath.Dir(binDir), "browser", "run"), workspace: workspace, closeTimeout: 10 * time.Second, alive: pidAlive}
	if err := m.validateName(m.Name()); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.runDir, 0700); err != nil {
		return nil, fmt.Errorf("create browser run directory: %w", err)
	}
	if err := os.MkdirAll(m.ScreenshotDir(), 0700); err != nil {
		return nil, fmt.Errorf("create screenshot directory: %w", err)
	}
	m.exec = m.execute
	return m, nil
}

func (m *Manager) Name() string   { return fmt.Sprintf("aice-%d-%d", m.pid, m.gen) }
func (m *Manager) RunDir() string { return m.runDir }
func (m *Manager) ScreenshotDir() string {
	return filepath.Join(m.workspace, ".aice", "browser", "screenshots")
}
func (m *Manager) Target() Target { return m.target }
func (m *Manager) validateName(name string) error {
	if len(filepath.Join(m.runDir, name+".sock")) > 103 {
		return fmt.Errorf("browser socket path exceeds 103 bytes: %s", m.runDir)
	}
	return nil
}
func (m *Manager) Rotate() error {
	next := fmt.Sprintf("aice-%d-%d", m.pid, m.gen+1)
	if err := m.validateName(next); err != nil {
		return err
	}
	m.gen++
	m.target = Target{}
	return nil
}
func (m *Manager) Environment() map[string]string {
	return map[string]string{
		"AGENT_BROWSER_SESSION":        m.Name(),
		"AGENT_BROWSER_SOCKET_DIR":     m.runDir,
		"AGENT_BROWSER_SCREENSHOT_DIR": m.ScreenshotDir(),
		"AGENT_BROWSER_SKILLS_DIR":     filepath.Join(m.binDir, "agent-browser-skills", deps.AgentBrowserVersion),
		"AGENT_BROWSER_CDP":            m.target.Endpoint,
		"AGENT_BROWSER_AUTO_CONNECT":   strconv.FormatBool(m.target.Auto),
	}
}
func (m *Manager) Executable() (string, error) {
	path := filepath.Join(m.binDir, "agent-browser")
	if !deps.AgentBrowserInstalled(m.binDir) {
		return path, ErrHelperMissing
	}
	return path, nil
}
func validateTarget(target Target) error {
	if target.Auto && target.Endpoint == "" {
		return nil
	}
	if target.Auto || target.Endpoint == "" {
		return fmt.Errorf("choose auto-detect or a CDP port/WebSocket URL")
	}
	if port, err := strconv.Atoi(target.Endpoint); err == nil && port > 0 && port <= 65535 {
		return nil
	}
	u, err := url.Parse(target.Endpoint)
	if err == nil && (u.Scheme == "ws" || u.Scheme == "wss") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(target.Endpoint, " \t\r\n") {
		return nil
	}
	return fmt.Errorf("invalid CDP endpoint: expected port 1–65535 or ws:// / wss:// URL")
}
func (m *Manager) Connect(ctx context.Context, target Target) ([]Tab, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	// Disconnect and rotate first so an old pinned target cannot leak into a new
	// browser, and a daemon finishing close cannot race the next connection.
	used := m.HasSidecar() || m.target != (Target{})
	if err := m.Close(ctx); err != nil {
		return nil, err
	}
	if used {
		if err := m.Rotate(); err != nil {
			return nil, err
		}
	}
	m.target = target
	args := []string{"--pin-tab", "tab", "list", "--json"}
	if target.Auto {
		args = append([]string{"--auto-connect"}, args...)
	} else {
		args = append([]string{"--cdp", target.Endpoint}, args...)
	}
	data, err := m.command(ctx, m.Name(), args...)
	if err != nil {
		return nil, err
	}
	return parseTabs(data)
}
func parseTabs(data []byte) ([]Tab, error) {
	var result struct {
		Tabs []Tab `json:"tabs"`
	}
	if err := decodeResult(data, &result); err != nil {
		return nil, err
	}
	return result.Tabs, nil
}
func (m *Manager) Tabs(ctx context.Context) ([]Tab, error) {
	if !m.HasSidecar() {
		return nil, fmt.Errorf("browser session is not running")
	}
	data, err := m.command(ctx, m.Name(), "tab", "list", "--json")
	if err != nil {
		return nil, err
	}
	return parseTabs(data)
}
func (m *Manager) BindTab(ctx context.Context, targetID string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(targetID) {
		return fmt.Errorf("invalid tab identifier")
	}
	data, err := m.command(ctx, m.Name(), "tab", targetID, "--json")
	if err != nil {
		return err
	}
	return decodeResult(data, nil)
}
func (m *Manager) NewTab(ctx context.Context) error {
	data, err := m.command(ctx, m.Name(), "tab", "new", "about:blank", "--json")
	if err != nil {
		return err
	}
	return decodeResult(data, nil)
}
func (m *Manager) Info(ctx context.Context) (Info, error) {
	data, err := m.command(ctx, m.Name(), "session", "info", "--json")
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := decodeResult(data, &info); err != nil {
		return Info{}, err
	}
	info.Raw = data
	return info, nil
}
func (m *Manager) HasSidecar() bool { return m.hasSidecar(m.Name()) }
func (m *Manager) hasSidecar(name string) bool {
	for _, suffix := range []string{".sock", ".pid"} {
		if _, err := os.Lstat(filepath.Join(m.runDir, name+suffix)); err == nil || !os.IsNotExist(err) {
			return true
		}
	}
	return false
}
func (m *Manager) Close(ctx context.Context) error {
	err := m.closeSession(ctx, m.Name())
	m.target = Target{}
	return err
}
func (m *Manager) closeSession(ctx context.Context, name string) error {
	if !m.hasSidecar(name) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, m.closeTimeout)
	defer cancel()
	data, err := m.command(ctx, name, "close", "--json")
	if err != nil {
		return err
	}
	return decodeResult(data, nil)
}
func decodeResult(data []byte, dest any) error {
	var result struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode browser response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("agent-browser: %s", result.Error)
	}
	if dest != nil {
		if err := json.Unmarshal(result.Data, dest); err != nil {
			return fmt.Errorf("decode browser data: %w", err)
		}
	}
	return nil
}
