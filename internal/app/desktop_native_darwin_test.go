//go:build integration && darwin

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Explicit opt-in only, after native setup. Only synthetic AppKit windows are
// addressed. This is scripted-model execution, not physical input or vision QA.
func TestNativeMacDesktopPrint(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	installed, err := deps.InstallCua(ctx, deps.DefaultOptions().WithBinDir(t.TempDir()).WithNoInstall(true))
	if err != nil {
		t.Fatal(err)
	}
	hostHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := desktopServiceEndpoint(hostHome)
	report, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint)
	if err != nil || !report.ConnectionVerified || report.Accessibility != desktop.PermissionGranted || report.ScreenRecording != desktop.PermissionGranted {
		t.Fatal("complete native setup and authorization before running the CLI test", err)
	}
	binary := buildMacPrintFixture(t, ctx)
	var targets []nativePrintFixture
	for i := range 3 {
		targets = append(targets, startMacPrintFixture(t, ctx, binary, fmt.Sprintf("Target%d", i), false))
	}
	sentinel := startMacPrintFixture(t, ctx, binary, "Sentinel", true)
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Active })
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":true,"desktop_control_mode":"background_only"}`)
	model := &nativePrintModel{t: t, targets: targets, query: "AICE CLI"}
	// General configuration/skill discovery stays in the isolated test HOME.
	// Only the real desktop constructor resolves the user's installed service;
	// no fake Manager, backend, Guard or Session is injected.
	nativeApp := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return hostHome, nil }}}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return model, nil },
		newDesktop: nativeApp.newDesktopState,
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
	initial := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Armed && s.Active })
	started := time.Now()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatal("native macOS CLI failed", err)
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
	afterCommand := awaitNativePrintState(t, ctx, sentinel, func(nativePrintState) bool { return true })
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Ticks > afterCommand.Ticks })
	if final.Ticks <= initial.Ticks || !final.Active || !final.Armed || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
		t.Fatal("CLI task disturbed foreground sentinel")
	}
	verifyNativePrintSession(t, ctx, sessionPath, model.results)
	// Command cleanup must leave the separately owned authorized service usable.
	if after, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint); err != nil || !after.ConnectionVerified {
		t.Fatal("shared Driver unavailable after command completion", err)
	}
	t.Logf("native macOS CLI: three AppKit commits in %s, nine PNG results replayed, focus_losses=0, shared service preserved", elapsed)
}

func buildMacPrintFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "fixture")
	command := exec.CommandContext(ctx, "/usr/bin/xcrun", "swiftc", "-module-cache-path", filepath.Join(directory, "cache"), "-o", binary, "../desktop/testdata/native-fixture.swift")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile synthetic AppKit fixture: %v\n%s", err, output)
	}
	return binary
}

// This verifies fixture preparation independently without opening applications,
// accessing the service, or requesting permissions.
func TestNativeMacPrintFixtureBuild(t *testing.T) {
	if os.Getenv("AICE_CUA_BUILD_FIXTURE") != "1" {
		t.Skip("set AICE_CUA_BUILD_FIXTURE=1 to compile only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	buildMacPrintFixture(t, ctx)
}

func startMacPrintFixture(t *testing.T, ctx context.Context, binary, label string, sentinel bool) nativePrintFixture {
	t.Helper()
	fixture := nativePrintFixture{directory: t.TempDir(), name: "AICE CLI " + label}
	contents := filepath.Join(fixture.directory, label+".app", "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(contents, "MacOS", "fixture")
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>fixture</string><key>CFBundleIdentifier</key><string>test.aice.cli.%s</string><key>CFBundleName</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`, strings.ToLower(label), label)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	mode := "target"
	if sentinel {
		mode = "sentinel"
	}
	command := exec.CommandContext(ctx, executable, fixture.directory, fixture.name, mode)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	fixture.pid = command.Process.Pid
	awaitNativePrintState(t, ctx, fixture, func(s nativePrintState) bool { return s.PID == fixture.pid })
	if sentinel {
		activationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(activationCtx, "/usr/bin/open", "-a", filepath.Dir(contents)).Run(); err != nil {
			t.Fatal("activate synthetic sentinel", err)
		}
	}
	return fixture
}
