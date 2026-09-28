//go:build integration && darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// This additional opt-in can read configured credentials and consume provider
// quota. Neither the integration tag nor AICE_CUA_NATIVE enables it.
func TestNativeMacActualModelDesktop(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE_MODEL") != "1" {
		t.Skip("requires separate approval and AICE_CUA_NATIVE_MODEL=1; uses a real model")
	}
	route := os.Getenv("AICE_CUA_MODEL_ROUTE")
	if route != "" && route != "managed" {
		t.Fatal("AICE_CUA_MODEL_ROUTE must be omitted or managed; the typed comparison route was removed after acceptance")
	}
	requestBudget := 20
	if raw, supplied := os.LookupEnv("AICE_CUA_MODEL_REQUEST_BUDGET"); supplied {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			t.Fatal("AICE_CUA_MODEL_REQUEST_BUDGET must be between 1 and 200")
		}
		requestBudget = parsed
	}
	providerID, modelID, thinking := os.Getenv("AICE_CUA_MODEL_PROVIDER"), os.Getenv("AICE_CUA_MODEL_ID"), os.Getenv("AICE_CUA_MODEL_THINKING")
	if providerID == "" || modelID == "" || thinking == "" {
		t.Fatal("explicit AICE_CUA_MODEL_PROVIDER, AICE_CUA_MODEL_ID and AICE_CUA_MODEL_THINKING are required")
	}
	tokenBudget := int64(100000)
	if raw, supplied := os.LookupEnv("AICE_CUA_MODEL_TOKEN_BUDGET"); supplied {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > 10000000 {
			t.Fatal("AICE_CUA_MODEL_TOKEN_BUDGET must be an explicitly authorized integer from 1 to 10000000")
		}
		tokenBudget = parsed
	}
	artifacts := os.Getenv("AICE_CUA_MODEL_ARTIFACT_DIR")
	entries, err := os.ReadDir(artifacts)
	if !filepath.IsAbs(artifacts) || err != nil || len(entries) != 0 {
		t.Fatal("AICE_CUA_MODEL_ARTIFACT_DIR must be a fresh empty absolute directory")
	}
	// No project settings or skills are loaded. Ordinary provider factories keep
	// their existing API-key/OAuth behavior; credentials never enter the prompt.
	configuration, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatal("load explicitly opted-in model configuration", err)
	}
	configuration.Provider, configuration.Model = providerID, modelID
	configuration.Thinking = llm.ThinkingLevel(thinking)
	providers := defaultProviders()
	model, options, err := resolveModelSettings(providers, configuration)
	if err != nil || !slices.Contains(model.InputModalities, llm.InputModalityImage) {
		t.Fatal("select a configured image-capable model", err)
	}
	if !providerConfigured(providers, configuration) {
		t.Fatal("selected provider has no configured credential")
	}
	service, err := modelForConfiguration(providers, configuration)
	if err != nil {
		t.Fatal("create opted-in provider", err)
	}
	testNativeMacModelTask(t, model, options, func([]nativePrintFixture) llm.Streamer { return service }, true, tokenBudget, requestBudget)
}

func testNativeMacModelTask(t *testing.T, model llm.Model, options llm.StreamOptions, factory func([]nativePrintFixture) llm.Streamer, actual bool, tokenBudget int64, requestBudget int) {
	t.Helper()
	options.MaxTokens = 4096
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	nativeApp := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return home, nil }}}
	configuration := config.Config{DesktopEnabled: true, DesktopControlMode: config.DesktopControlMode(desktop.BackgroundOnly), NoDepInstall: true}
	state, err := nativeApp.newDesktopState(configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if report, err := state.inspect(ctx); err != nil || !report.ConnectionVerified || report.Accessibility != desktop.PermissionGranted || report.ScreenRecording != desktop.PermissionGranted {
		t.Fatal("complete native setup before the model task", err)
	}
	appKit := buildMacPrintFixture(t, ctx)
	webKit := buildMacPrintFixtureSource(t, ctx, "webkit-fixture.swift")
	prefix := fmt.Sprintf("Model%d", os.Getpid())
	var targets []nativePrintFixture
	for i, binary := range []string{appKit, webKit, appKit} {
		targets = append(targets, startMacPrintFixture(t, ctx, binary, fmt.Sprintf("%sTarget%d", prefix, i), false))
	}
	sentinel := startMacPrintFixture(t, ctx, appKit, prefix+"Sentinel", true)
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Active })
	timings := &nativeModelTimings{}
	scope := &nativeModelScope{timings: timings, targets: targets, captured: make(map[string]bool)}
	bind := state.bind
	state.bind = func(ctx context.Context, options desktop.RunOptions) (managedDesktopRun, func() error, error) {
		backend, closeRun, err := bind(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		scope.managedDesktopRun = backend
		return scope, closeRun, err
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
	gate, guard, err := newExecutionGuard(workspace.Path(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	countedGuard := &nativeModelGuard{Guard: guard, timings: timings}
	catalog, err := state.managedCatalog(runCtx, config.MCPConfig{}, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	guard.mcp = catalog
	search, err := tool.NewToolSearch(catalog)
	if err != nil {
		t.Fatal(err)
	}
	skills := discoverSkills("", "", false)
	reader, err := tool.NewToolResultRead(runResultReader{})
	if err != nil {
		t.Fatal(err)
	}
	taskTools := appendSkillTool([]agent.Tool{search, reader}, skills.catalog)
	systemPrompt := appendSkillsPrompt(buildDefaultSystemPrompt(taskTools, workspace.Path()), skills.catalog)
	viewBudget := llm.ResultViewBudget(model.ContextWindow)
	observer := &nativeModelObserver{timings: timings, Streamer: factory(targets), model: model, results: make(map[string]llm.ToolResultMessage)}
	limits := agent.RunLimits{MaxTurns: requestBudget, Tokens: tokenBudget, Timeout: 5 * time.Minute, NoProgress: 3}
	loop, err := agent.NewLoop(observer, taskTools, agent.WithGuard(countedGuard), agent.WithRunLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	if actual {
		artifacts = os.Getenv("AICE_CUA_MODEL_ARTIFACT_DIR")
	}
	store, _, _, err := prepareSession(ctx, workspace, filepath.Join(artifacts, "model-task.jsonl"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runCtx, err = modelSessionContext(runCtx, store)
	if err != nil {
		t.Fatal(err)
	}
	runCtx = withResultReader(runCtx, store, nil)
	var prompt strings.Builder
	prompt.WriteString("Use the computer-use Skill and managed:cua tool discovery for this task. Only list_windows with a listed PID, get_window_state and background input on the listed windows are in scope. Do not use list_apps or launch_app. Use tool_result_read if an observation is clipped. ")
	fmt.Fprint(&prompt, "Complete this synthetic desktop acceptance task. Only the listed windows are in scope. Request a screenshot when observing each target and after each action. In order, replace each Task value field with its assigned text, click Commit exactly once, and inspect the returned result. The middle window is a WebKit form with an initially empty input; the others are native AppKit fields. Do not launch or close apps, use foreground assistance, or act on the Sentinel. Report any uncertainty instead of repeating an unknown action.\n")
	for i, target := range targets {
		fmt.Fprintf(&prompt, "%d. Window %q, PID %d: %q\n", i+1, target.name, target.pid, nativePrintValue(i))
	}
	if err := os.WriteFile(filepath.Join(artifacts, "task.txt"), []byte(prompt.String()), 0600); err != nil {
		t.Fatal(err)
	}
	message, err := llm.NewUserMessage(llm.NewTextContent(prompt.String()).Part())
	if err != nil {
		t.Fatal(err)
	}
	writeNativePrintSignal(t, sentinel, "arm")
	awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Armed && s.Active })
	started := time.Now()
	var focusSamples []nativeModelFocusSample
	captureFocus := func(phase string) {
		data, err := os.ReadFile(filepath.Join(sentinel.directory, "state.json"))
		var state nativePrintState
		if err != nil || json.Unmarshal(data, &state) != nil {
			t.Error("sentinel diagnostic state unavailable", err)
			return
		}
		sample := nativeModelFocusState(phase, nativeMS(time.Since(started)), state, sentinel, targets)
		sample.ToolSequence = len(timings.Tools)
		if len(focusSamples) > 0 {
			previous := focusSamples[len(focusSamples)-1]
			if sample.Active != previous.Active || sample.FocusLosses != previous.FocusLosses || sample.Foreground != previous.Foreground || sample.ValueState != previous.ValueState {
				t.Logf("sentinel state changed: %+v", sample)
			}
		}
		focusSamples = append(focusSamples, sample)
	}
	captureFocus("before_loop")
	result, runErr := loop.Run(runCtx, agent.RunInput{Model: model, Options: options, Prompt: message,
		SystemPrompt: systemPrompt, Catalog: catalog, ResultViewTokens: viewBudget,
		MessageRecorder: func(ctx context.Context, message llm.AgentMessage) error {
			return appendSessionMessage(ctx, store, message)
		},
	}, func(ctx context.Context, event agent.AgentEvent) error {
		if err := timings.event(ctx, event); err != nil {
			return err
		}
		switch event.Type {
		case agent.EventTypeToolExecutionStart:
			captureFocus("before_tool")
		case agent.EventTypeToolExecutionEnd:
			captureFocus("after_tool")
		}
		return nil
	})
	captureFocus("after_loop")
	elapsed := time.Since(started)
	accepted := false
	serviceVerified, replayVerified := false, false
	t.Logf("actual_model=%v provider=%s model=%s thinking=%s driver=%s elapsed=%s requests=%d images=%d guard_asks=%d scope_refusals=%d reported_tokens=%d", actual, model.Provider, model.ID, options.Thinking, desktop.DriverVersion, elapsed, observer.requests, observer.images, countedGuard.asks, scope.refusals, result.Usage.TotalTokens)
	defer func() {
		const route = "managed"
		network := "scripted decisions; no model network"
		if actual {
			network = "configured provider transport; network latency not isolated"
		}
		report := struct {
			Route                                      string            `json:"route"`
			Provider                                   llm.ProviderID    `json:"provider"`
			Model                                      string            `json:"model"`
			Thinking                                   llm.ThinkingLevel `json:"thinking"`
			ElapsedMS                                  int64             `json:"elapsed_ms"`
			Requests, Images, GuardAsks, ScopeRefusals int
			Usage                                      llm.Usage `json:"usage"`
			LoopCompleted                              bool      `json:"loop_completed"`
			Accepted                                   bool      `json:"accepted"`
			ActualModel                                bool      `json:"actual_model"`
			Platform, Architecture, Driver, Network    string
			Timings                                    *nativeModelTimings      `json:"timings"`
			Limits                                     map[string]int64         `json:"limits"`
			Focus                                      []nativeModelFocusSample `json:"focus"`
			ServiceVerified                            bool                     `json:"service_verified"`
			ReplayVerified                             bool                     `json:"replay_verified"`
		}{
			Route: route, Provider: model.Provider, Model: model.ID, Thinking: options.Thinking,
			ElapsedMS: elapsed.Milliseconds(), Requests: observer.requests, Images: observer.images,
			GuardAsks: countedGuard.asks, ScopeRefusals: scope.refusals, Usage: result.Usage,
			LoopCompleted: runErr == nil, Accepted: accepted && !t.Failed(), ActualModel: actual,
			Platform: runtime.GOOS, Architecture: runtime.GOARCH, Driver: desktop.DriverVersion,
			Network: network, Timings: timings, Focus: focusSamples,
			ServiceVerified: serviceVerified, ReplayVerified: replayVerified,
			Limits: map[string]int64{
				"reported_tokens": limits.Tokens, "model_requests": int64(limits.MaxTurns), "result_view_tokens": viewBudget,
				"timeout_ms": limits.Timeout.Milliseconds(), "output_tokens_per_response": int64(options.MaxTokens),
			},
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifacts, "report.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if actual {
			t.Logf("actual-model artifacts retained at %s", artifacts)
		}
		// Read back the actual report format in the scripted gate as well.
		var saved struct {
			Route   string                   `json:"route"`
			Timings nativeModelTimings       `json:"timings"`
			Limits  map[string]int64         `json:"limits"`
			Focus   []nativeModelFocusSample `json:"focus"`
		}
		data, err = os.ReadFile(filepath.Join(artifacts, "report.json"))
		if err != nil || json.Unmarshal(data, &saved) != nil || saved.Route != route || len(saved.Timings.Requests) != observer.requests || len(saved.Timings.Tools) != len(timings.Tools) || !reflect.DeepEqual(saved.Limits, report.Limits) || !reflect.DeepEqual(saved.Focus, report.Focus) {
			t.Error("timing report or limits did not survive JSON serialization", err)
		}
	}()
	if runErr != nil {
		t.Fatal("bounded model task did not complete", runErr)
	}
	for i, target := range targets {
		value := nativePrintValue(i)
		awaitNativePrintState(t, ctx, target, func(s nativePrintState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
	}
	timings.verify(t, observer.requests, len(observer.results))
	if !actual {
		// The scripted model emits one call per request except its final reply,
		// including any local result-readback requests.
		for i, request := range timings.Requests {
			if (request.PreparationMS != nil) != (i > 0) || (request.FirstToolCallMS != nil) != (i < len(timings.Requests)-1) {
				t.Fatal("scripted timing gate missed request preparation or tool output")
			}
		}
		managedCalls := 0
		for _, call := range timings.Tools {
			if call.ManagedCallMS != nil {
				managedCalls++
			}
		}
		if managedCalls != 18 {
			t.Fatal("scripted timing gate did not retain all managed native calls")
		}
		decisions := observer.Streamer.(*nativeManagedCUAModel)
		if !decisions.loaded || decisions.index != 3 || decisions.nativeCalls != 18 || observer.images != 9 {
			t.Fatal("managed model harness omitted Skill, native actions or screenshots")
		}
	}
	if countedGuard.asks != 0 || scope.refusals != 0 || observer.images < 3 || len(scope.captured) != len(targets) {
		t.Fatal("model task needed approval, left its scope, or lacked images")
	}
	catalog.Close()
	if err := closeRun(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	previous := awaitNativePrintState(t, ctx, sentinel, func(nativePrintState) bool { return true })
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Ticks > previous.Ticks+3 })
	focusSamples = append(focusSamples, nativeModelFocusState("after_cleanup", nativeMS(time.Since(started)), final, sentinel, targets))
	if !final.Active || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
		t.Errorf("model task disturbed the foreground sentinel: %+v", focusSamples[len(focusSamples)-1])
	}
	if report, err := state.inspect(ctx); err != nil || !report.ConnectionVerified {
		t.Fatal("model task cleanup stopped the shared service", err)
	}
	serviceVerified = true
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	verifyNativeModelReplayView(t, ctx, store.Path(), observer.results, viewBudget)
	replayVerified = true
	t.Log("shared service and exact tool-result/image Session replay verified")
	accepted = true
}

// The fixture samples every 50 ms; event-boundary reads can share a tick and do
// not attribute the cause of a focus change. No PID, app name or input is retained.
type nativeModelFocusSample struct {
	Phase          string  `json:"phase"`
	ElapsedMS      float64 `json:"elapsed_ms"`
	Ticks          int     `json:"ticks"`
	Active         bool    `json:"active"`
	FocusLosses    int     `json:"focus_losses"`
	Foreground     string  `json:"foreground"`
	ValueUnchanged bool    `json:"value_unchanged"`
	Commits        int     `json:"commits"`
	ToolSequence   int     `json:"tool_sequence"`
	ValueState     string  `json:"value_state"`
}

func nativeModelFocusState(phase string, elapsedMS float64, state nativePrintState, sentinel nativePrintFixture, targets []nativePrintFixture) nativeModelFocusSample {
	foreground := "other"
	switch {
	case state.FrontPID == sentinel.pid:
		foreground = "sentinel"
	case state.FrontPID == 0:
		foreground = "unknown"
	case state.FrontIsLogin:
		foreground = "loginwindow"
	default:
		for i, target := range targets {
			if state.FrontPID == target.pid {
				foreground = fmt.Sprintf("target_%d", i+1)
				break
			}
		}
	}
	value := "other"
	if state.Value == "AICE-314" {
		value = "baseline"
	} else {
		for i := range targets {
			if strings.Contains(state.Value, nativePrintValue(i)) {
				value = fmt.Sprintf("contains_task_value_%d", i+1)
				break
			}
		}
	}
	return nativeModelFocusSample{Phase: phase, ElapsedMS: elapsedMS, Ticks: state.Ticks, Active: state.Active,
		FocusLosses: state.FocusLosses, Foreground: foreground, ValueUnchanged: state.Value == "AICE-314", Commits: state.Commits, ValueState: value}
}

// Scope assertions belong only to this opt-in test. They never replace native
// results or introduce an application allowlist in the product.
type nativeModelScope struct {
	managedDesktopRun
	timings  *nativeModelTimings
	targets  []nativePrintFixture
	captured map[string]bool
	refusals int
}

func (s *nativeModelScope) refuse() error {
	s.refusals++
	return errors.New("synthetic model task scope refused the request before native dispatch")
}

type nativeModelGuard struct {
	agent.Guard
	timings *nativeModelTimings
	asks    int
}

func (g *nativeModelGuard) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	started := time.Now()
	result, err := g.Guard.Check(ctx, call)
	current := g.timings.active
	if current != nil {
		elapsed := nativeMS(time.Since(started))
		current.GuardMS = &elapsed
		if result.Revalidate != nil {
			revalidate := result.Revalidate
			result.Revalidate = func(ctx context.Context) error {
				started := time.Now()
				err := revalidate(ctx)
				elapsed := nativeMS(time.Since(started))
				current.RevalidateMS = &elapsed
				return err
			}
		}
	}
	if result.Decision == agent.GuardAsk {
		g.asks++
	}
	return result, err
}

type nativeModelObserver struct {
	llm.Streamer
	timings          *nativeModelTimings
	model            llm.Model
	requests, images int
	results          map[string]llm.ToolResultMessage
}

func (m *nativeModelObserver) ProviderID() llm.ProviderID {
	if identity, ok := m.Streamer.(agent.ModelIdentity); ok {
		return identity.ProviderID()
	}
	return m.model.Provider
}

func (m *nativeModelObserver) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.requests++
	for _, message := range request.Messages {
		if result, ok := message.(llm.ToolResultMessage); ok {
			if _, seen := m.results[result.ToolCallID]; seen {
				continue
			}
			m.results[result.ToolCallID] = result
			for _, part := range result.Content {
				if part.Image != nil {
					m.images++
				}
			}
		}
	}
	measurement := m.timings.startRequest()
	stream, err := m.Streamer.Stream(ctx, request)
	if err != nil || stream == nil {
		elapsed := nativeMS(time.Since(measurement.started))
		measurement.FinishedMS = &elapsed
		return stream, err
	}
	return &nativeModelTimedStream{Stream: stream, measurement: measurement}, nil
}

func verifyNativeModelReplay(t *testing.T, ctx context.Context, path string, delivered map[string]llm.ToolResultMessage) {
	t.Helper()
	verifyNativeModelReplayView(t, ctx, path, delivered, 0)
}

func verifyNativeModelReplayView(t *testing.T, ctx context.Context, path string, delivered map[string]llm.ToolResultMessage, viewBudget int64) {
	t.Helper()
	store, err := session.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	parent := ""
	pending := make(map[string]string)
	count := 0
	for _, entry := range snapshot.Messages {
		if entry.ID == "" || entry.ParentID != parent {
			t.Fatal("broken actual-model Session chain")
		}
		parent = entry.ID
		switch message := entry.Message.(type) {
		case llm.AssistantMessage:
			for _, part := range message.Content {
				if part.ToolCall != nil {
					pending[part.ToolCall.ID] = part.ToolCall.Name
				}
			}
		case llm.ToolResultMessage:
			if pending[message.ToolCallID] != message.ToolName || !reflect.DeepEqual(llm.BoundToolResultView(message, viewBudget), delivered[message.ToolCallID]) {
				t.Fatal("actual-model tool result/image did not survive replay")
			}
			delete(pending, message.ToolCallID)
			count++
		}
	}
	if len(pending) != 0 || count != len(delivered) || count == 0 || snapshot.LeafID != parent {
		t.Fatal("actual-model Session has incomplete tool pairs")
	}
}
