//go:build integration && linux

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Real private installation -> CLI -> Guard/Loop -> tools -> native Manager ->
// Session replay. The model is scripted; PNG delivery is not visual reasoning.
// This opt-in touches only synthetic windows in the disposable X11 runner.
func TestNativeLinuxDesktopPrint(t *testing.T) {
	if os.Getenv("AICE_CUA_X11_CONTAINER") != "1" {
		t.Skip("requires the explicitly isolated Linux X11 fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Getenv("DISPLAY") != ":99" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("requires the test container's Xvfb :99 and private session bus")
	}
	fixture := os.Getenv("AICE_CUA_TEST_LINUX_FIXTURE")
	archive := os.Getenv("AICE_CUA_TEST_NATIVE_ARCHIVE")
	if !filepath.IsAbs(fixture) || !filepath.IsAbs(archive) {
		t.Fatal("requires absolute paths to the checked-in GTK fixture and pinned archive")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home := t.TempDir()
	t.Setenv("HOME", home)
	binary := installLinuxPrintDriver(t, ctx, home, archive)
	var targets []nativePrintFixture
	for i := range 3 {
		targets = append(targets, startLinuxPrintFixture(t, ctx, fixture, fmt.Sprintf("AICE CLI Target %d", i), "target"))
	}
	sentinel := startLinuxPrintFixture(t, ctx, fixture, "AICE CLI Sentinel", "sentinel")
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Active })
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":true,"desktop_control_mode":"background_only"}`)
	model := &nativePrintModel{t: t, targets: targets, query: "AICE CLI Target"}
	command, err := newTestCommand(t, dependencies{
		loadConfig:  func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:    func(config.Config) (llm.Streamer, error) { return model, nil },
		userHomeDir: func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	sessionPath := filepath.Join(t.TempDir(), "desktop.jsonl")
	command.SetArgs([]string{"--workspace", t.TempDir(), "--session", sessionPath, "--no-dep-install", "--no-update-check", "--print", "Commit the synthetic values in the three AICE CLI target windows."})
	writeNativePrintSignal(t, sentinel, "arm")
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.KeysSent >= 3 })
	started := time.Now()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatal("native desktop CLI failed", err)
	}
	elapsed := time.Since(started)
	if model.requests != 11 || len(model.results) != 10 || strings.TrimSpace(output.String()) != "Synthetic tool sequence finished." {
		t.Fatal("unexpected CLI completion or model request count", model.requests, len(model.results))
	}
	for i, target := range targets {
		value := nativePrintValue(i)
		awaitNativePrintState(t, ctx, target, func(s nativePrintState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
		if strings.Contains(output.String()+diagnostics.String(), value) {
			t.Fatal("native payload appeared in CLI output")
		}
	}
	writeNativePrintSignal(t, sentinel, "stop-typing")
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Value == strings.Repeat("a", s.KeysSent) })
	if !final.Active || final.FocusLosses != 0 || final.Commits != 0 || final.KeysSent < 3 {
		t.Fatal("CLI actions disturbed concurrent foreground input")
	}
	verifyNativePrintSession(t, ctx, sessionPath, model.results)
	// Only this private installation can match; unrelated services are untouched.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); err == nil && executable == binary {
			t.Fatal("owned native child survived command completion", entry.Name())
		}
	}
	t.Logf("native %s: CLI completed three GTK commits in %s; nine PNG results replayed; %d concurrent keys retained; owned Driver reaped", runtime.GOARCH, elapsed, final.KeysSent)
}

type linuxPrintArchiveTransport struct{ path, url string }

func (r linuxPrintArchiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet || request.URL.String() != r.url {
		return nil, errors.New("unexpected native fixture download request")
	}
	file, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: file, ContentLength: info.Size(), Header: make(http.Header)}, nil
}

func installLinuxPrintDriver(t *testing.T, ctx context.Context, home, archive string) string {
	t.Helper()
	artifact, err := deps.CuaDriverArtifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	options := deps.DefaultOptions().WithBinDir(filepath.Join(home, ".aice", "bin"))
	options.Client = &http.Client{Transport: linuxPrintArchiveTransport{path: archive,
		url: "https://github.com/trycua/cua/releases/download/cua-driver-rs-v" + deps.CuaDriverVersion + "/" + artifact.Name}}
	installed, err := deps.InstallCua(ctx, options)
	if err != nil || !installed.Installed || installed.Reused || len(installed.Warnings) != 0 {
		t.Fatal("private native installation failed", err)
	}
	return installed.Installation.Binary
}

func startLinuxPrintFixture(t *testing.T, ctx context.Context, script, name, mode string) nativePrintFixture {
	t.Helper()
	fixture := nativePrintFixture{directory: t.TempDir(), name: name}
	command := exec.CommandContext(ctx, "/usr/bin/python3", script, fixture.directory, name, mode)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	fixture.pid = command.Process.Pid
	awaitNativePrintState(t, ctx, fixture, func(nativePrintState) bool { return true })
	return fixture
}
