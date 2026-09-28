//go:build integration && darwin

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// Managed three-form task with the full result view; the bounded-view model
// harness separately exercises clipping and tool_result_read.
// Scripted decisions exercise integration, not a model's interpretation of the
// Skill. No provider credentials, network model or third-party app input.
func TestNativeMacManagedCUAFullView(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	app := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return home, nil }}}
	configuration := config.Config{DesktopEnabled: true, DesktopControlMode: config.DesktopControlMode(desktop.BackgroundOnly), NoDepInstall: true}
	state, err := app.newDesktopState(configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if status, err := state.inspect(ctx); err != nil || !status.ConnectionVerified || status.Accessibility != desktop.PermissionGranted || status.ScreenRecording != desktop.PermissionGranted {
		t.Fatal("complete native setup before the managed task", err)
	}
	nativeBinary := buildMacPrintFixture(t, ctx)
	webBinary := buildMacPrintFixtureSource(t, ctx, "webkit-fixture.swift")
	var targets []nativePrintFixture
	for i, binary := range []string{nativeBinary, webBinary, nativeBinary} {
		targets = append(targets, startMacPrintFixture(t, ctx, binary, fmt.Sprintf("ManagedTarget%d", i), false))
	}
	sentinel := startMacPrintFixture(t, ctx, nativeBinary, "ManagedSentinel", true)
	model, options, err := resolveModelSettings(defaultProviders(), config.Config{Provider: "custom", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, closeRun, err := state.bindContext(ctx, configuration, model)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closeRun(); err != nil {
			t.Error(err)
		}
	}()
	workspace, err := tool.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gate, adapter, err := newExecutionGuard(workspace.Path(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := state.managedCatalog(runCtx, config.MCPConfig{}, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	adapter.mcp = catalog
	search, err := tool.NewToolSearch(catalog)
	if err != nil {
		t.Fatal(err)
	}
	skills := discoverSkills("", "", false)
	taskTools := appendSkillTool([]agent.Tool{search}, skills.catalog)
	decisions := &nativeManagedCUAModel{targets: targets, names: make(map[string]string), inputActions: map[int]string{1: "type_text"}}
	timings := &nativeModelTimings{}
	observer := &nativeModelObserver{Streamer: decisions, timings: timings, model: model, results: make(map[string]llm.ToolResultMessage)}
	guarded := &nativeModelGuard{Guard: adapter, timings: timings}
	loop, err := agent.NewLoop(observer, taskTools, agent.WithGuard(guarded), agent.WithRunLimits(agent.RunLimits{MaxTurns: 40, Timeout: 2 * time.Minute}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "managed-full-view.jsonl")
	store, _, _, err := prepareSession(ctx, workspace, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	prompt, _ := llm.NewUserMessage(llm.NewTextContent("Load computer-use, then fill and commit the three assigned synthetic windows once each. Verify each postcondition using fresh state.").Part())
	writeNativePrintSignal(t, sentinel, "arm")
	initial := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Armed && s.Active })
	started := time.Now()
	_, runErr := loop.Run(runCtx, agent.RunInput{Model: model, Options: options, Prompt: prompt, Catalog: catalog, SystemPrompt: appendSkillsPrompt(buildDefaultSystemPrompt(taskTools, workspace.Path()), skills.catalog), MessageRecorder: func(ctx context.Context, message llm.AgentMessage) error {
		return appendSessionMessage(ctx, store, message)
	}}, timings.event)
	if runErr != nil {
		t.Fatal("managed task did not complete", runErr)
	}
	if !decisions.loaded || decisions.index != 3 || decisions.nativeCalls != 18 || observer.images != 9 || guarded.asks != 0 {
		t.Fatal("incomplete managed task accounting", decisions.index, decisions.nativeCalls, observer.images, guarded.asks)
	}
	for i, target := range targets {
		value := nativePrintValue(i)
		awaitNativePrintState(t, ctx, target, func(s nativePrintState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
	}
	elapsed := time.Since(started)
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Ticks > initial.Ticks })
	if !final.Active || !final.Armed || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
		t.Error("managed route disturbed foreground sentinel")
	}
	if err := closeRun(); err != nil {
		t.Error(err)
	}
	if after, err := state.inspect(ctx); err != nil || !after.ConnectionVerified {
		t.Error("shared service unavailable after managed cleanup", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	verifyNativeModelReplay(t, ctx, path, observer.results)
	t.Logf("managed full view: driver=%s loop=%s requests=%d native_calls=%d images=%d guard_asks=%d focus_losses=%d; widget postconditions, one commit per target, shared service and exact Session replay checked", desktop.DriverVersion, elapsed, observer.requests, decisions.nativeCalls, observer.images, guarded.asks, final.FocusLosses)
}
