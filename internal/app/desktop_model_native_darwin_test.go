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
	providerID, modelID, thinking := os.Getenv("AICE_CUA_MODEL_PROVIDER"), os.Getenv("AICE_CUA_MODEL_ID"), os.Getenv("AICE_CUA_MODEL_THINKING")
	if providerID == "" || modelID == "" || thinking == "" {
		t.Fatal("explicit AICE_CUA_MODEL_PROVIDER, AICE_CUA_MODEL_ID and AICE_CUA_MODEL_THINKING are required")
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
	testNativeMacModelTask(t, model, options, func([]nativePrintFixture, string) llm.Streamer { return service }, true)
}

// Exercises the same harness without loading credentials or calling a model.
func TestNativeMacModelHarness(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 to verify the model harness with scripted decisions")
	}
	model, options, err := resolveModelSettings(defaultProviders(), config.Config{Provider: "custom", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	testNativeMacModelTask(t, model, options, func(targets []nativePrintFixture, query string) llm.Streamer {
		return &nativePrintModel{t: t, targets: targets, query: query, inputActions: map[int]string{1: "type_text"}}
	}, false)
}

func testNativeMacModelTask(t *testing.T, model llm.Model, options llm.StreamOptions, factory func([]nativePrintFixture, string) llm.Streamer, actual bool) {
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
	query := "AICE CLI " + prefix + "Target"
	timings := &nativeModelTimings{}
	scope := &nativeModelScope{timings: timings, query: query, targets: targets, refs: make(map[string]bool), observations: make(map[string]bool), captured: make(map[string]bool)}
	bind := state.bind
	state.bind = func(ctx context.Context, options desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
		backend, closeRun, err := bind(ctx, options)
		scope.DesktopBackend = backend
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
	gate.SetDesktopEnabled(configuration.DesktopEnabled)
	guard.desktop = state
	countedGuard := &nativeModelGuard{Guard: guard, timings: timings}
	typed, err := tool.NewDesktopTools(state)
	if err != nil {
		t.Fatal(err)
	}
	var taskTools []agent.Tool
	for _, capability := range typed {
		taskTools = append(taskTools, capability)
	}
	observer := &nativeModelObserver{timings: timings, Streamer: factory(targets, query), model: model, results: make(map[string]llm.ToolResultMessage)}
	loop, err := agent.NewLoop(observer, taskTools, agent.WithGuard(countedGuard), agent.WithRunLimits(agent.RunLimits{
		MaxTurns: 20, Tokens: 100000, Timeout: 5 * time.Minute, NoProgress: 3,
	}))
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
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Complete this synthetic desktop acceptance task using only the three desktop tools. Discover with the exact query %q. Only the listed windows are in scope. Request a screenshot when observing each target and after each action. In order, replace each Task value field with its assigned text, click Commit exactly once, and inspect the returned result. The middle window is a WebKit form with an initially empty input; the others are native AppKit fields. Do not launch or close apps, use foreground assistance, or act on the Sentinel. Report any uncertainty instead of repeating an unknown action.\n", query)
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
	result, runErr := loop.Run(runCtx, agent.RunInput{Model: model, Options: options, Prompt: message,
		SystemPrompt: buildDefaultSystemPrompt(taskTools, workspace.Path()),
		MessageRecorder: func(ctx context.Context, message llm.AgentMessage) error {
			return appendSessionMessage(ctx, store, message)
		},
	}, timings.event)
	elapsed := time.Since(started)
	accepted := false
	t.Logf("actual_model=%v provider=%s model=%s thinking=%s driver=%s elapsed=%s requests=%d images=%d guard_asks=%d scope_refusals=%d reported_tokens=%d", actual, model.Provider, model.ID, options.Thinking, desktop.DriverVersion, elapsed, observer.requests, observer.images, countedGuard.asks, scope.refusals, result.Usage.TotalTokens)
	defer func() {
		network := "scripted decisions; no model network"
		if actual {
			network = "configured provider transport; network latency not isolated"
		}
		report := struct {
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
			Timings                                    *nativeModelTimings `json:"timings"`
		}{
			Provider: model.Provider, Model: model.ID, Thinking: options.Thinking,
			ElapsedMS: elapsed.Milliseconds(), Requests: observer.requests, Images: observer.images,
			GuardAsks: countedGuard.asks, ScopeRefusals: scope.refusals, Usage: result.Usage,
			LoopCompleted: runErr == nil, Accepted: accepted && !t.Failed(), ActualModel: actual,
			Platform: runtime.GOOS, Architecture: runtime.GOARCH, Driver: desktop.DriverVersion,
			Network: network, Timings: timings,
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
			Timings nativeModelTimings `json:"timings"`
		}
		data, err = os.ReadFile(filepath.Join(artifacts, "report.json"))
		if err != nil || json.Unmarshal(data, &saved) != nil || len(saved.Timings.Requests) != observer.requests || len(saved.Timings.Tools) != len(timings.Tools) {
			t.Error("timing report did not survive JSON serialization", err)
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
		// This known scripted sequence has no retries and returns one complete
		// action event in each of its first ten provider requests.
		for i, request := range timings.Requests {
			if (request.PreparationMS != nil) != (i > 0) || (request.FirstToolCallMS != nil) != (i < 10) {
				t.Fatal("scripted timing gate missed request preparation or action output")
			}
		}
		actions := 0
		for _, call := range timings.Tools {
			if call.Action != nil {
				actions++
			}
		}
		if actions != 6 {
			t.Fatal("scripted timing gate did not retain all six native actions")
		}
	}
	if countedGuard.asks != 0 || scope.refusals != 0 || observer.images < 3 || len(scope.captured) != len(targets) {
		t.Fatal("model task needed approval, left its scope, or lacked images")
	}
	if err := closeRun(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	previous := awaitNativePrintState(t, ctx, sentinel, func(nativePrintState) bool { return true })
	final := awaitNativePrintState(t, ctx, sentinel, func(s nativePrintState) bool { return s.Ticks > previous.Ticks+3 })
	if !final.Active || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
		t.Fatal("model task disturbed the foreground sentinel")
	}
	if report, err := state.inspect(ctx); err != nil || !report.ConnectionVerified {
		t.Fatal("model task cleanup stopped the shared service", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	verifyNativeModelReplay(t, ctx, store.Path(), observer.results)
	accepted = true
}

// Scope assertions belong only to this opt-in test. They never replace native
// results or introduce an application allowlist in the product.
type nativeModelScope struct {
	tool.DesktopBackend
	timings                      *nativeModelTimings
	query                        string
	targets                      []nativePrintFixture
	refs, observations, captured map[string]bool
	refusals                     int
}

func (s *nativeModelScope) refuse() error {
	s.refusals++
	return errors.New("synthetic model task scope refused the request before native dispatch")
}

func (s *nativeModelScope) Apps(ctx context.Context, query string, limit int) (desktop.Discovery, error) {
	if query != s.query {
		return desktop.Discovery{}, s.refuse()
	}
	result, err := s.DesktopBackend.Apps(ctx, query, limit)
	if err != nil {
		return result, err
	}
	result.Apps = nil
	windows := result.Windows[:0]
	for _, window := range result.Windows {
		for _, target := range s.targets {
			if window.PID == target.pid && window.Title == target.name {
				windows = append(windows, window)
				s.refs[window.Ref] = true
			}
		}
	}
	result.Windows = windows
	return result, nil
}

func (s *nativeModelScope) Observe(ctx context.Context, request desktop.ObserveRequest) (desktop.Observation, error) {
	if !s.refs[request.TargetRef] {
		return desktop.Observation{}, s.refuse()
	}
	result, err := s.DesktopBackend.Observe(ctx, request)
	if err == nil {
		s.observations[result.Ref] = true
		if result.Image != nil {
			s.captured[request.TargetRef] = true
		}
	}
	return result, err
}

func (s *nativeModelScope) Act(ctx context.Context, request desktop.ActRequest) (desktop.ActResult, error) {
	if !s.observations[request.ObservationRef] || (request.DeliveryMode != "" && request.DeliveryMode != "background") {
		return desktop.ActResult{}, s.refuse()
	}
	switch request.Kind {
	case "click", "type_text", "set_value", "wait":
	case "hotkey":
		if !slices.Equal(request.Keys, []string{"cmd", "a"}) {
			return desktop.ActResult{}, s.refuse()
		}
	case "key":
		if request.Key != "delete" {
			return desktop.ActResult{}, s.refuse()
		}
	default:
		return desktop.ActResult{}, s.refuse()
	}
	delete(s.observations, request.ObservationRef)
	result, err := s.DesktopBackend.Act(ctx, request)
	if s.timings != nil && s.timings.active != nil {
		s.timings.active.Action = &nativeModelActionTiming{
			Kind: request.Kind, TotalMS: nativeMS(result.Timing.Total), QueueMS: nativeMS(result.Timing.Queue),
			DriverMS: nativeMS(result.Timing.Driver), ConditionWaitMS: nativeMS(result.Timing.ConditionWait), ObservationMS: nativeMS(result.Timing.Observation),
		}
	}
	if result.Observation != nil {
		s.observations[result.Observation.Ref] = true
	}
	return result, err
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
			if pending[message.ToolCallID] != message.ToolName || !reflect.DeepEqual(message, delivered[message.ToolCallID]) {
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

func TestNativeModelScopeRejectsUnownedActions(t *testing.T) {
	t.Parallel()
	backend := &appDesktopBackend{result: desktop.ActResult{Outcome: "returned", Observation: &desktop.Observation{Ref: "fresh"}}}
	scope := &nativeModelScope{DesktopBackend: backend, query: "synthetic", refs: map[string]bool{"owned": true}, observations: map[string]bool{"observed": true}}
	if _, err := scope.Apps(t.Context(), "", 16); err == nil {
		t.Fatal("broad discovery accepted")
	}
	if _, err := scope.Observe(t.Context(), desktop.ObserveRequest{TargetRef: "foreign"}); err == nil {
		t.Fatal("foreign window accepted")
	}
	for _, request := range []desktop.ActRequest{
		{Kind: "click", ObservationRef: "foreign"},
		{Kind: "click", ObservationRef: "observed", DeliveryMode: "foreground"},
		{Kind: "launch", ObservationRef: "observed", AppRef: "application"},
		{Kind: "hotkey", ObservationRef: "observed", Keys: []string{"cmd", "q"}},
		{Kind: "key", ObservationRef: "observed", Key: "return"},
	} {
		if _, err := scope.Act(t.Context(), request); err == nil {
			t.Fatal("out-of-scope native action accepted")
		}
	}
	if backend.calls != 0 || scope.refusals != 7 {
		t.Fatal("scope failure reached native backend")
	}
	if _, err := scope.Act(t.Context(), desktop.ActRequest{Kind: "type_text", ObservationRef: "observed", Text: "synthetic"}); err != nil || backend.calls != 1 {
		t.Fatal("owned input rejected", err)
	}
	if _, err := scope.Act(t.Context(), desktop.ActRequest{Kind: "type_text", ObservationRef: "observed", Text: "duplicate"}); err == nil || backend.calls != 1 {
		t.Fatal("consumed observation replayed")
	}
	if _, err := scope.Act(t.Context(), desktop.ActRequest{Kind: "click", ObservationRef: "fresh"}); err != nil || backend.calls != 2 {
		t.Fatal("fresh owned observation rejected", err)
	}
}
