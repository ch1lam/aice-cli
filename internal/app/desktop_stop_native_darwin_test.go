//go:build integration && darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// Real CLI, Settings Stop, Loop, Guard and Manager. Only the model is scripted;
// the wrapper records lifecycle facts without substituting any native result.
func TestNativeMacDesktopStopTUI(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens a synthetic window")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
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
	report, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint)
	if err != nil || !report.ConnectionVerified || report.Accessibility != desktop.PermissionGranted || report.ScreenRecording != desktop.PermissionGranted {
		t.Fatal("complete native setup and authorization before the Stop gate", err)
	}
	target := startMacPrintFixture(t, ctx, buildMacPrintFixture(t, ctx), "Stop", false)
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":true,"desktop_control_mode":"background_only"}`)
	model := &nativeStopModel{target: target}
	backend := &nativeStopBackend{started: make(chan struct{}), finished: make(chan nativeStopCompletion, 1)}
	closed := make(chan error, 1)
	var binds, closes atomic.Int32
	nativeApp := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return hostHome, nil }}}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return model, nil },
		newDesktop: func(c config.Config) (*desktopState, error) {
			state, err := nativeApp.newDesktopState(c)
			if err != nil {
				return nil, err
			}
			bind := state.bind
			state.bind = func(ctx context.Context, options desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
				if binds.Add(1) != 1 {
					return nil, nil, errors.New("unexpected additional run binding")
				}
				run, closeRun, err := bind(ctx, options)
				if err != nil {
					return nil, nil, err
				}
				backend.DesktopBackend = run
				return backend, func() error {
					wasCancelled := errors.Is(ctx.Err(), context.Canceled)
					err := closeRun()
					if !wasCancelled {
						err = errors.Join(err, errors.New("native cleanup preceded run cancellation"))
					}
					closes.Add(1)
					closed <- err
					return err
				}, nil
			}
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
	sessionPath := filepath.Join(t.TempDir(), "stop.jsonl")
	command.SetArgs([]string{"--workspace", t.TempDir(), "--session", sessionPath, "--no-approve", "--no-dep-install", "--no-update-check"})
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); reader.Close(); input.Close(); <-stopped })
	width := 120
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	waitFor := func(want string) {
		t.Helper()
		waitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		var recent strings.Builder
		for {
			select {
			case <-ticker.C:
				send("")
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				if strings.Contains(plain, nativeStopCondition) {
					t.Fatal("folded condition appeared in the default UI")
				}
				recent.WriteString(plain)
				if strings.Contains(recent.String(), want) {
					return
				}
			case err := <-done:
				t.Fatalf("command stopped before %q: %v\n%s", want, err, recent.String())
			case <-waitCtx.Done():
				t.Fatalf("missing %q: %s", want, recent.String())
			}
		}
	}
	send("")
	waitFor("AICE")
	send("Observe the synthetic Stop window and wait for its test condition.\r")
	waitFor("· Waiting")
	select {
	case <-backend.started:
	case <-ctx.Done():
		t.Fatal("native wait did not start")
	}
	send("/desktop\r")
	waitFor("Stop current run")
	// Esc must only close Settings; the native wait must remain pending.
	send("\x1b")
	waitFor("· Waiting")
	send("/desktop\r")
	waitFor("Stop current run")
	select {
	case <-backend.finished:
		t.Fatal("native wait ended before explicit Stop")
	default:
	}
	started := time.Now()
	send("\x1b[17~") // Settings-local F6 activates Stop current run.
	send("\x1b")
	waitFor("Response cancelled")
	elapsed := time.Since(started)
	select {
	case completion := <-backend.finished:
		if !errors.Is(completion.ctxErr, context.Canceled) || completion.result.Dispatched || completion.result.Timing.ConditionWait <= 0 {
			t.Fatalf("native wait did not poll and settle under cancellation: dispatched=%v polling=%s context=%v", completion.result.Dispatched, completion.result.Timing.ConditionWait, completion.ctxErr)
		}
	case <-ctx.Done():
		t.Fatal("native wait did not settle")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("run did not close")
	}
	if elapsed > 5*time.Second || binds.Load() != 1 || closes.Load() != 1 || backend.calls.Load() != 1 || model.requests.Load() != 3 {
		t.Fatal("Stop was unbounded, duplicated input/cleanup or resumed the model", elapsed, model.requests.Load())
	}
	configuration, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || !configuration.DesktopEnabled || configuration.DesktopControlMode != "background_only" {
		t.Fatal("Stop changed saved desktop preferences", err)
	}
	send("\x15/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
	state := awaitNativePrintState(t, ctx, target, func(nativePrintState) bool { return true })
	state = awaitNativePrintState(t, ctx, target, func(s nativePrintState) bool { return s.Ticks > state.Ticks+3 })
	if state.Commits != 0 || state.Value != "AICE-314" || state.Result != "Result: pending" {
		t.Fatal("Stop task altered the synthetic controls")
	}
	verifyNativeStopSession(t, ctx, sessionPath)
	if report, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("Stop or command cleanup stopped shared service", err)
	}
	t.Logf("native Settings Stop: cancellation visible in %s, one native wait, one cancelled binding cleanup, three model requests, complete Session tool pairs, zero commits, shared service preserved", elapsed)
}

const nativeStopCondition = "AICE private impossible Stop condition"

type nativeStopCompletion struct {
	result desktop.ActResult
	ctxErr error
}

type nativeStopBackend struct {
	tool.DesktopBackend
	started  chan struct{}
	finished chan nativeStopCompletion
	calls    atomic.Int32
}

func (b *nativeStopBackend) Act(ctx context.Context, request desktop.ActRequest) (desktop.ActResult, error) {
	if b.calls.Add(1) != 1 || request.Kind != "wait" {
		return desktop.ActResult{}, errors.New("unexpected native action")
	}
	close(b.started)
	result, err := b.DesktopBackend.Act(ctx, request)
	b.finished <- nativeStopCompletion{result: result, ctxErr: ctx.Err()}
	return result, err
}

type nativeStopModel struct {
	target   nativePrintFixture
	requests atomic.Int32
}

func (m *nativeStopModel) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	step := m.requests.Add(1) - 1
	call := func(name string, arguments any) (llm.Stream, error) {
		data, err := json.Marshal(arguments)
		if err != nil {
			return nil, err
		}
		return toolCallEventStream(request.Model, llm.ToolCall{ID: fmt.Sprintf("stop-%d", step), Name: name, Arguments: data}), nil
	}
	if step == 0 {
		return call("desktop_apps", map[string]any{"query": m.target.name, "limit": 16})
	}
	result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if !ok || result.IsError || result.ToolCallID != fmt.Sprintf("stop-%d", step-1) || len(result.Content) == 0 || result.Content[0].Type != llm.ContentTypeText {
		return nil, errors.New("native Stop sequence received an invalid result")
	}
	switch step {
	case 1:
		var discovery desktop.Discovery
		if err := json.Unmarshal([]byte(result.Content[0].Text), &discovery); err != nil {
			return nil, err
		}
		ref := ""
		for _, window := range discovery.Windows {
			if window.PID == m.target.pid && window.Title == m.target.name {
				if ref != "" {
					return nil, errors.New("ambiguous Stop target")
				}
				ref = window.Ref
			}
		}
		if ref == "" {
			return nil, errors.New("exact Stop target missing")
		}
		return call("desktop_observe", desktop.ObserveRequest{TargetRef: ref, Screenshot: true})
	case 2:
		var observation desktop.Observation
		if err := json.Unmarshal([]byte(result.Content[0].Text), &observation); err != nil {
			return nil, err
		}
		if observation.Ref == "" || observation.Degraded || len(result.Content) != 2 || result.Content[1].Image == nil {
			return nil, errors.New("native Stop observation or capture missing")
		}
		return call("desktop_act", desktop.ActRequest{Kind: "wait", ObservationRef: observation.Ref, Wait: &desktop.WaitCondition{Text: nativeStopCondition, TimeoutMS: 10000}})
	default:
		return nil, errors.New("model unexpectedly resumed after native wait")
	}
}

func verifyNativeStopSession(t *testing.T, ctx context.Context, path string) {
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
	var parent string
	pending := make(map[string]string)
	results, captures := 0, 0
	for _, entry := range snapshot.Messages {
		if entry.ID == "" || entry.ParentID != parent {
			t.Fatal("Stop broke durable history parents")
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
			if pending[message.ToolCallID] != message.ToolName {
				t.Fatal("Stop left an unmatched native tool result")
			}
			delete(pending, message.ToolCallID)
			results++
			if message.ToolName == "desktop_act" && !message.IsError {
				t.Fatal("cancelled condition wait was recorded as successful")
			}
			for _, part := range message.Content {
				if part.Type == llm.ContentTypeImage && part.Image != nil {
					captures++
				}
			}
		}
	}
	if len(pending) != 0 || results != 3 || captures != 1 || snapshot.LeafID != parent {
		t.Fatal("Stop did not retain three complete tool pairs and the native capture", results, captures)
	}
}
