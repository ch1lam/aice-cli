//go:build integration && darwin

package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Separate opt-in: unlike task gates, setup invokes the public grant/capture
// flow and may show OS UI. It reuses an already installed, authorized service;
// only AICE configuration is isolated. No real model or Session is used.
func TestNativeMacDesktopSetupTUI(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE_SETUP") != "1" {
		t.Skip("set AICE_CUA_NATIVE_SETUP=1 to exercise explicit native setup with existing OS grants")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	hostHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := deps.InstallCua(ctx, deps.DefaultOptions().WithBinDir(t.TempDir()).WithNoInstall(true))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := desktopServiceEndpoint(hostHome)
	before, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint)
	if err != nil || !before.ConnectionVerified || before.Accessibility != desktop.PermissionGranted || before.ScreenRecording != desktop.PermissionGranted {
		t.Fatal("complete native installation and OS authorization before the setup reuse gate", err)
	}
	home, err := os.MkdirTemp("/private/tmp", "aice-mac-setup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	workspace := t.TempDir()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":false}`)
	model := &recordingModel{response: "unexpected model request"}
	owners := make(chan *desktopState, 1)
	var installs, setups atomic.Int32
	nativeApp := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return hostHome, nil }}}
	command, err := newTestCommand(t, dependencies{
		loadConfig:   func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:     func(config.Config) (llm.Streamer, error) { return model, nil },
		userHomeDir:  func() (string, error) { return home, nil },
		saveSettings: config.SaveSettingsFile,
		newDesktop: func(c config.Config) (*desktopState, error) {
			state, err := nativeApp.newDesktopState(c)
			if err != nil {
				return nil, err
			}
			install, setup := state.install, state.setup
			state.install = func(ctx context.Context, options deps.Options) (deps.CuaInstallResult, error) {
				installs.Add(1)
				result, err := install(ctx, options)
				if err == nil && (!result.Reused || result.Installed) {
					return result, fmt.Errorf("native setup did not reuse the verified installation")
				}
				return result, err
			}
			state.setup = func(ctx context.Context, binary string, options desktop.SetupOptions) (desktop.SetupResult, error) {
				setups.Add(1)
				return setup(ctx, binary, options)
			}
			owners <- state
			return state, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	stopClosing := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()); input.CloseWithError(ctx.Err()) })
	defer stopClosing()
	output := loginTerminalOutput{ctx: ctx, frames: make(chan string, 256)}
	command.SetIn(reader)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--workspace", workspace, "--no-approve", "--no-dep-install", "--no-update-check"})
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	width := 120
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	var transcript strings.Builder
	waitFor := func(want string) {
		t.Helper()
		timeout := 20 * time.Second
		if want == "Computer Use enabled for the next run" {
			timeout = 4 * time.Minute
		}
		waitCtx, cancelWait := context.WithTimeout(ctx, timeout)
		defer cancelWait()
		var recent strings.Builder
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				send("")
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				transcript.WriteString(plain)
				recent.WriteString(plain)
				if strings.Contains(recent.String(), want) {
					t.Logf("Settings reached %q", want)
					return
				}
			case err := <-done:
				t.Fatalf("command stopped waiting for %q: %v\n%s", want, err, transcript.String())
			case <-waitCtx.Done():
				t.Fatalf("terminal never displayed %q\n%s", want, transcript.String())
			}
		}
	}
	send("")
	waitFor("AICE")
	var owner *desktopState
	select {
	case owner = <-owners:
	case <-ctx.Done():
		t.Fatal("native desktop owner was not created")
	}
	captureRecorded := func() bool {
		owner.healthMu.Lock()
		defer owner.healthMu.Unlock()
		return !owner.setupCaptureAt.IsZero()
	}
	send("/desktop\r")
	waitFor("Computer Use control mode")
	send("/Computer Use setup\r")
	waitFor("Enable preference only")
	send("\r")
	waitFor("outside this project")
	waitFor("Continue?")
	if installs.Load() != 0 || setups.Load() != 0 || captureRecorded() {
		t.Fatal("setup performed external work before confirmation")
	}
	// Cancel is initially selected. No installation, public grant/capture flow
	// or preference write may happen before the separate retry is confirmed.
	send("\r")
	waitFor("Computer Use setup cancelled; no changes made")
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || loaded.DesktopEnabled || installs.Load() != 0 || setups.Load() != 0 || captureRecorded() {
		t.Fatal("cancelled setup performed work or enabled Computer Use", err)
	}
	// The single-line cancellation notice leaves the Settings list open.
	send("/Computer Use setup\r")
	waitFor("Enable preference only")
	send("\r")
	waitFor("Continue?")
	send("\x1b[B\r")
	waitFor("Computer Use enabled for the next run")
	if installs.Load() != 1 || setups.Load() != 1 || !captureRecorded() {
		t.Fatal("setup did not establish native capture through exactly one reuse/setup operation")
	}
	send("\x1b")
	send("/Computer Use status\r")
	waitFor("Accessibility: Granted")
	waitFor("Screen Recording: Granted")
	waitFor("Capture verification: Succeeded")
	send("\x1b")
	send("\x1b")
	send("/session\r")
	waitFor("Not started")
	send("\x1b")
	send("\x15/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native setup command did not quit")
	}
	loaded, err = config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || !loaded.DesktopEnabled || len(model.requests) != 0 {
		t.Fatal("native setup persistence or model isolation failed", err)
	}
	if err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			return fmt.Errorf("setup unexpectedly created a Session")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint); err != nil || !after.ConnectionVerified {
		t.Fatal("native setup cleanup stopped the shared service", err)
	}
	t.Log("native macOS Settings: cancel before external work, verified installation reuse, public grant/capture flow, persisted temporary enable, no model/Session, shared service preserved")
}
