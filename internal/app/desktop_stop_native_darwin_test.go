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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// Real CLI, Settings Stop, Loop, Guard and Manager. Only the model is scripted;
// the wrapper records lifecycle facts without substituting any native result.
func TestNativeMacDesktopStopTUI(t *testing.T) {
	testNativeMacDesktopStopTUI(t, false)
}

// Stop after independent widget state proves a click committed, while the
// native response is still pending. No response is delayed by the harness.
func TestNativeMacDesktopStopMutationTUI(t *testing.T) {
	testNativeMacDesktopStopTUI(t, true)
}

func testNativeMacDesktopStopTUI(t *testing.T, mutation bool) {
	t.Helper()
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
	model := &nativeStopModel{target: target, mutation: mutation, ready: make(chan struct{}), release: make(chan struct{}), names: make(map[string]string)}
	backend := &nativeStopBackend{mutation: mutation, started: make(chan struct{}), finished: make(chan nativeStopCompletion, 1)}
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
			state.bind = func(ctx context.Context, options desktop.RunOptions) (managedDesktopRun, func() error, error) {
				if binds.Add(1) != 1 {
					return nil, nil, errors.New("unexpected additional run binding")
				}
				run, closeRun, err := bind(ctx, options)
				if err != nil {
					return nil, nil, err
				}
				backend.managedDesktopRun = run
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
	send("Perform the synthetic Stop acceptance task.\r")
	// Open Settings while the scripted model is between native operations.
	select {
	case <-model.ready:
	case <-ctx.Done():
		t.Fatal("model did not receive native observation")
	}
	send("/desktop\r")
	waitFor("Stop current run")
	// Esc only closes Settings, without cancelling the active model/Run.
	send("\x1b")
	send("/desktop\r")
	waitFor("Stop current run")
	close(model.release)
	if mutation {
		awaitNativePrintState(t, ctx, target, func(s nativePrintState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
		select {
		case <-backend.finished:
			t.Fatal("native mutation ended before explicit Stop")
		default:
		}
	} else {
		select {
		case <-backend.started:
		case <-ctx.Done():
			t.Fatal("explicit native read polling did not start")
		}
	}

	started := time.Now()
	send("\x1b[17~") // Settings-local F6 activates Stop current run.
	send("\x1b")
	waitFor("Response cancelled")
	elapsed := time.Since(started)
	var completed nativeStopCompletion
	if mutation {
		select {
		case completed = <-backend.finished:
			if !errors.Is(completed.ctxErr, context.Canceled) || completed.result.State != llm.ExecutionUnknown {
				t.Fatal("native mutation did not settle with unknown outcome under cancellation", completed.ctxErr, completed.result.State)
			}
		case <-ctx.Done():
			t.Fatal("native mutation did not settle")
		}
	}

	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("run did not close")
	}
	if elapsed > 5*time.Second || binds.Load() != 1 || closes.Load() != 1 || mutation && (backend.calls.Load() != 1 || model.requests.Load() != int32(4+model.readback.calls)) || !mutation && backend.calls.Load() != 0 {
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
	wantCommits, wantResult := 0, "Result: pending"
	var retained *mcpclient.Result
	if mutation {
		wantCommits, wantResult, retained = 1, "Result: AICE-314", &completed.result
	}
	if state.Commits != wantCommits || state.Value != "AICE-314" || state.Result != wantResult {
		t.Fatal("Stop task lost/replayed input or altered unrelated synthetic controls")
	}
	verifyNativeStopSession(t, ctx, sessionPath, retained, model.readback.calls)
	if report, err := desktop.Inspect(ctx, installed.Installation.Binary, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("Stop or command cleanup stopped shared service", err)
	}
	t.Logf("native Settings Stop: cancellation visible in %s, mutation=%v outcome=%s commits=%d, cancelled binding cleanup, complete model tool pairing, complete Session tool pairs, shared service preserved", elapsed, mutation, completed.result.State, state.Commits)
}

const nativeStopCondition = "AICE private impossible Stop condition"

type nativeStopCompletion struct {
	result mcpclient.Result
	ctxErr error
}
type nativeStopBackend struct {
	managedDesktopRun
	mutation     bool
	started      chan struct{}
	finished     chan nativeStopCompletion
	calls, reads atomic.Int32
}

func (b *nativeStopBackend) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if name == "get_window_state" && b.reads.Add(1) == 2 && !b.mutation {
		close(b.started)
	}
	if name != "click" {
		return b.managedDesktopRun.CallChecked(ctx, name, args, check)
	}
	if !b.mutation || b.calls.Add(1) != 1 {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, errors.New("unexpected native mutation")
	}
	close(b.started)
	result, err := b.managedDesktopRun.CallChecked(ctx, name, args, check)
	b.finished <- nativeStopCompletion{result: result, ctxErr: ctx.Err()}
	return result, err
}

type nativeStopModel struct {
	readback       nativeManagedReadback
	step           int
	mutation       bool
	ready, release chan struct{}
	target         nativePrintFixture
	requests       atomic.Int32
	names          map[string]string
	window         uint64
}

func (m *nativeStopModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	m.requests.Add(1)
	step := m.step
	if step > 0 {
		last, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
		if !ok {
			return nil, errors.New("missing Stop result")
		}
		result, stream, err := m.readback.consume(request.Model, last)
		if stream != nil || err != nil {
			return stream, err
		}
		request.Messages = append([]llm.Message(nil), request.Messages...)
		request.Messages[len(request.Messages)-1] = result
	}
	m.step++
	call := func(name string, args any) (llm.Stream, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		return toolCallEventStream(request.Model, llm.ToolCall{ID: fmt.Sprintf("stop-%d", step), Name: name, Arguments: raw}), nil
	}
	if step == 0 {
		return call("tool_search", tool.ToolSearchRequest{Service: managedCUAKey, IDs: []string{mcpToolID(managedCUAKey, "list_windows"), mcpToolID(managedCUAKey, "get_window_state"), mcpToolID(managedCUAKey, "click")}, Limit: 3})
	}
	result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if !ok || result.IsError || result.ToolCallID != fmt.Sprintf("stop-%d", step-1) {
		return nil, errors.New("invalid Stop tool result")
	}
	if step == 1 {
		var found tool.ToolSearchResult
		if len(result.Content) == 0 || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil {
			return nil, errors.New("missing Stop discovery")
		}
		for _, entry := range found.Entries {
			m.names[strings.TrimPrefix(entry.ID, managedCUAKey+"/tool/")] = entry.Name
		}
		if len(m.names) != 3 {
			return nil, errors.New("missing selected Stop tools")
		}
		return call(m.names["list_windows"], map[string]any{"pid": m.target.pid})
	}
	if result.Details == nil || result.Details.Binding == nil || result.Details.Binding.Source != "managed:computer-use" || result.Details.State != llm.ExecutionReturned {
		return nil, errors.New("missing Stop native provenance")
	}
	if step == 2 {
		var found struct {
			Windows []struct {
				PID    int    `json:"pid"`
				Window uint64 `json:"window_id"`
				Title  string `json:"title"`
			} `json:"windows"`
		}
		if json.Unmarshal(nativeManagedJSON(result), &found) != nil {
			return nil, errors.New("invalid Stop windows")
		}
		for _, window := range found.Windows {
			if window.PID == m.target.pid && window.Title == m.target.name {
				if m.window != 0 {
					return nil, errors.New("ambiguous Stop window")
				}
				m.window = window.Window
			}
		}
		if m.window == 0 {
			return nil, errors.New("missing exact Stop window")
		}
		return call(m.names["get_window_state"], map[string]any{"pid": m.target.pid, "window_id": m.window, "include_screenshot": true})
	}
	if step > 3 && m.mutation {
		return nil, errors.New("model resumed after native mutation")
	}
	var observed struct {
		PID      int               `json:"pid"`
		Window   uint64            `json:"window_id"`
		Elements []desktop.Element `json:"elements"`
	}
	if json.Unmarshal(nativeManagedJSON(result), &observed) != nil || observed.PID != m.target.pid || observed.Window != m.window {
		return nil, errors.New("invalid Stop observation")
	}
	if step == 3 {
		images := 0
		for _, part := range result.Content {
			if part.Image != nil {
				images++
			}
		}
		if images != 1 {
			return nil, errors.New("missing initial Stop image")
		}
		close(m.ready)
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if m.mutation {
			token := ""
			for _, element := range observed.Elements {
				if element.Label == "Commit" && element.Role == "AXButton" && element.Token != "" {
					if token != "" {
						return nil, errors.New("ambiguous Stop button")
					}
					token = element.Token
				}
			}
			if token == "" {
				return nil, errors.New("missing exact Stop button")
			}
			return call(m.names["click"], map[string]any{"pid": m.target.pid, "window_id": m.window, "element_token": token})
		}
	}
	// Waiting is now model-owned read polling, not a typed wait action.
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return call(m.names["get_window_state"], map[string]any{"pid": m.target.pid, "window_id": m.window, "include_screenshot": false, "query": nativeStopCondition})
}

func verifyNativeStopSession(t *testing.T, ctx context.Context, path string, retained *mcpclient.Result, readbacks int) {
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
			if managedDesktopOperation(message.ToolName) == "click" {
				if !message.IsError {
					t.Fatal("cancelled native action was recorded as successful")
				}
				if retained != nil {
					if message.Details == nil || message.Details.State != retained.State || !reflect.DeepEqual(message.Details.StructuredContent, retained.StructuredContent) {
						t.Fatal("Session lost unknown native dispatch provenance")
					}
					mapped, err := tool.NewMCP(tool.MCPOptions{Definition: llm.ToolDefinition{Name: message.ToolName, Description: "retained native result", InputSchema: []byte(`{"type":"object"}`)}, Binding: *message.Details.Binding, Backend: nativeStopRetainedResult{*retained}})
					if err != nil {
						t.Fatal(err)
					}
					value, err := mapped.Execute(ctx, llm.ToolCall{ID: message.ToolCallID, Name: message.ToolName, Arguments: []byte(`{}`)})
					if err != nil {
						t.Fatal(err)
					}
					expected, err := llm.NewToolResultMessage(value)
					if err != nil {
						t.Fatal(err)
					}
					expected.Timestamp = message.Timestamp
					if !reflect.DeepEqual(message, expected) {
						t.Fatal("Session changed the mapped unknown native result")
					}

				}
			}
			for _, part := range message.Content {
				if part.Type == llm.ContentTypeImage && part.Image != nil {
					captures++
				}
			}
		}
	}
	if len(pending) != 0 || results < 3 || retained != nil && results != 4+readbacks || captures != 1 || snapshot.LeafID != parent {
		t.Fatal("Stop did not retain complete tool pairs and the native capture", results, captures)
	}
}

// Reconstruct the expected generic projection from the retained native result,
// without contacting the Driver or replaying input.
type nativeStopRetainedResult struct{ result mcpclient.Result }

func (b nativeStopRetainedResult) Call(context.Context, string, json.RawMessage) (mcpclient.Result, error) {
	return b.result, context.Canceled
}
