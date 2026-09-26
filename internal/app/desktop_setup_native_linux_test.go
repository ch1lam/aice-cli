//go:build integration && linux

package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Real Settings UI -> private installer -> native selected-window capture ->
// persisted enable. Only archive delivery is replaced; no native backend is fake.
func TestNativeLinuxDesktopSetupTUI(t *testing.T) {
	if os.Getenv("AICE_CUA_X11_CONTAINER") != "1" {
		t.Skip("requires the explicitly isolated Linux X11 fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Getenv("DISPLAY") != ":99" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("requires the test container's Xvfb :99 and private session bus")
	}
	fixture, archive := os.Getenv("AICE_CUA_TEST_LINUX_FIXTURE"), os.Getenv("AICE_CUA_TEST_NATIVE_ARCHIVE")
	if !filepath.IsAbs(fixture) || !filepath.IsAbs(archive) {
		t.Fatal("requires absolute paths to the GTK fixture and pinned archive")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	sentinel := startLinuxPrintFixture(t, ctx, fixture, "AICE Setup Sentinel", "sentinel")
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Active })
	artifact, err := deps.CuaDriverArtifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	transport := &linuxSetupArchiveTransport{source: linuxPrintArchiveTransport{path: archive,
		url: "https://github.com/trycua/cua/releases/download/cua-driver-rs-v" + deps.CuaDriverVersion + "/" + artifact.Name}}
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":false}`)
	model := &recordingModel{response: "unexpected model request"}
	owners := make(chan *desktopState, 1)
	nativeApp := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return home, nil }}}
	command, err := newTestCommand(t, dependencies{
		loadConfig:   func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:     func(config.Config) (llm.Streamer, error) { return model, nil },
		userHomeDir:  func() (string, error) { return home, nil },
		saveSettings: config.SaveSettingsFile,
		newDesktop: func(c config.Config) (*desktopState, error) {
			state, err := nativeApp.newDesktopState(c)
			if err == nil {
				state.installOptions.Client = &http.Client{Transport: transport}
				owners <- state
			}
			return state, err
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
	command.SetArgs([]string{"--workspace", workspace, "--no-approve", "--no-update-check"})
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
					return
				}
			case err := <-done:
				t.Fatalf("command stopped while waiting for %q: %v\n%s", want, err, transcript.String())
			case <-ctx.Done():
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
	writeNativePrintSignal(t, sentinel, "arm")
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.KeysSent >= 3 })
	send("/desktop\r")
	waitFor("[Tools & Network]")
	send("/Computer Use setup\r")
	waitFor("Enable preference only")
	send("\r")
	waitFor("outside this project")
	waitFor("Continue?")
	if transport.requests.Load() != 0 || captureRecorded() {
		t.Fatal("setup performed external work before confirmation")
	}
	send("\x1b[B\r")
	waitFor("Window capture test")
	waitFor(sentinel.name)
	waitFor(fmt.Sprintf("Process %d", sentinel.pid))
	if transport.requests.Load() != 1 || captureRecorded() {
		t.Fatal("installation or explicit capture selection boundary mismatch")
	}
	// Cancel is the default. Installation remains, but no capture or enable
	// should be published, and the temporary native runtime must be released.
	send("\r")
	waitFor("context canceled")
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || loaded.DesktopEnabled || captureRecorded() {
		t.Fatal("cancelled window selection published capture or enable", err)
	}
	installed, err := deps.InstallCua(ctx, deps.DefaultOptions().WithBinDir(filepath.Join(home, ".aice", "bin")).WithNoInstall(true))
	if err != nil || !installed.Reused {
		t.Fatal("cancelled setup lost the verified installation", err)
	}
	assertNoNativeSetupProcess(t, installed.Installation.Binary)
	send("\x1b")
	send("/Computer Use setup\r")
	waitFor("Enable preference only")
	send("\r")
	waitFor("Continue?")
	send("\x1b[B\r")
	waitFor("Window capture test")
	waitFor(sentinel.name)
	waitFor(fmt.Sprintf("Process %d", sentinel.pid))
	if transport.requests.Load() != 1 || captureRecorded() {
		t.Fatal("setup retry downloaded again or captured before selection")
	}
	// This isolated display contains only the synthetic sentinel window.
	// Cancel is initially selected; Down explicitly chooses that window.
	send("\x1b[B\r")
	waitFor("Computer Use enabled for the next run")
	if !captureRecorded() {
		t.Fatal("setup succeeded without native capture evidence")
	}
	send("\x1b")
	send("/Computer Use status\r")
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
		t.Fatal("setup command did not quit")
	}
	loaded, err = config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || !loaded.DesktopEnabled || transport.requests.Load() != 1 || len(model.requests) != 0 {
		t.Fatal("setup persistence, download count or model isolation failed", err)
	}
	if err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			return fmt.Errorf("setup unexpectedly created a Session")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	installed, err = deps.InstallCua(ctx, deps.DefaultOptions().WithBinDir(filepath.Join(home, ".aice", "bin")).WithNoInstall(true))
	if err != nil || !installed.Reused {
		t.Fatal("setup did not publish a reusable verified private installation", err)
	}
	assertNoNativeSetupProcess(t, installed.Installation.Binary)
	writeNativePrintSignal(t, sentinel, "stop-typing")
	after := awaitNativePrintState(t, ctx, sentinel, func(nativePrintState) bool { return true })
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Ticks > after.Ticks+3 })
	if !final.Active || final.FocusLosses != 0 || final.Commits != 0 || final.Value != strings.Repeat("a", final.KeysSent) {
		t.Fatal("native setup disturbed the foreground fixture")
	}
	t.Logf("native setup UI: one private installation, selection cancelled and retried, explicit capture, persisted enable, no model/Session, Driver reaped; %d core keys retained", final.KeysSent)
}

func assertNoNativeSetupProcess(t *testing.T, binary string) {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); err == nil && executable == binary {
			t.Fatal("setup retained an owned native process")
		}
	}
}

type linuxSetupArchiveTransport struct {
	source   linuxPrintArchiveTransport
	requests atomic.Int32
}

func (r *linuxSetupArchiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.requests.Add(1)
	return r.source.RoundTrip(request)
}
