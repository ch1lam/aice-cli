//go:build integration && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
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
	var targets []linuxPrintFixture
	for i := range 3 {
		targets = append(targets, startLinuxPrintFixture(t, ctx, fixture, fmt.Sprintf("AICE CLI Target %d", i), "target"))
	}
	sentinel := startLinuxPrintFixture(t, ctx, fixture, "AICE CLI Sentinel", "sentinel")
	awaitLinuxPrintState(t, ctx, sentinel, func(s linuxPrintState) bool { return s.Active })
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":true,"desktop_control_mode":"background_only"}`)
	model := &linuxPrintModel{t: t, targets: targets}
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
	writeLinuxPrintSignal(t, sentinel, "arm")
	awaitLinuxPrintState(t, ctx, sentinel, func(s linuxPrintState) bool { return s.KeysSent >= 3 })
	started := time.Now()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatal("native desktop CLI failed", err)
	}
	elapsed := time.Since(started)
	if model.requests != 11 || len(model.results) != 10 || strings.TrimSpace(output.String()) != "Synthetic tool sequence finished." {
		t.Fatal("unexpected CLI completion or model request count", model.requests, len(model.results))
	}
	for i, target := range targets {
		value := linuxPrintValue(i)
		awaitLinuxPrintState(t, ctx, target, func(s linuxPrintState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
		if strings.Contains(output.String()+diagnostics.String(), value) {
			t.Fatal("native payload appeared in CLI output")
		}
	}
	writeLinuxPrintSignal(t, sentinel, "stop-typing")
	final := awaitLinuxPrintState(t, ctx, sentinel, func(s linuxPrintState) bool { return s.Value == strings.Repeat("a", s.KeysSent) })
	if !final.Active || final.FocusLosses != 0 || final.Commits != 0 || final.KeysSent < 3 {
		t.Fatal("CLI actions disturbed concurrent foreground input")
	}
	verifyLinuxPrintSession(t, ctx, sessionPath, model.results)
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

type linuxPrintFixture struct {
	directory, name string
	pid             int
}
type linuxPrintState struct {
	Active      bool   `json:"active"`
	FocusLosses int    `json:"focus_losses"`
	KeysSent    int    `json:"keys_sent"`
	Commits     int    `json:"commits"`
	Value       string `json:"value"`
	Result      string `json:"result"`
}

func startLinuxPrintFixture(t *testing.T, ctx context.Context, script, name, mode string) linuxPrintFixture {
	t.Helper()
	fixture := linuxPrintFixture{directory: t.TempDir(), name: name}
	command := exec.CommandContext(ctx, "/usr/bin/python3", script, fixture.directory, name, mode)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	fixture.pid = command.Process.Pid
	awaitLinuxPrintState(t, ctx, fixture, func(linuxPrintState) bool { return true })
	return fixture
}

func writeLinuxPrintSignal(t *testing.T, fixture linuxPrintFixture, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.directory, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitLinuxPrintState(t *testing.T, ctx context.Context, fixture linuxPrintFixture, check func(linuxPrintState) bool) linuxPrintState {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(fixture.directory, "state.json"))
		if err == nil {
			var state linuxPrintState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if check(state) {
				return state
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("synthetic fixture postcondition not reached", fixture.name)
		case <-tick.C:
		}
	}
}

// Scripted model consumes only real tool outputs. It never accesses the fixture
// readback or Driver directly, and never invents target or observation tokens.
type linuxPrintModel struct {
	t        *testing.T
	targets  []linuxPrintFixture
	windows  []desktop.Window
	requests int
	results  []llm.ToolResultMessage
}

func linuxPrintValue(index int) string { return fmt.Sprintf("AICE CLI stage %d 中文 ✓", index+1) }

func (m *linuxPrintModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	step := m.requests
	m.requests++
	call := func(name string, value any) llm.Stream {
		data, err := json.Marshal(value)
		if err != nil {
			m.t.Fatal(err)
		}
		return toolCallEventStream(request.Model, llm.ToolCall{ID: fmt.Sprintf("native-%d", step), Name: name, Arguments: data})
	}
	if step == 0 {
		return call("desktop_apps", map[string]any{"query": "AICE CLI Target", "limit": 16}), nil
	}
	result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if !ok || result.IsError || result.ToolCallID != fmt.Sprintf("native-%d", step-1) {
		return nil, fmt.Errorf("native tool result failed or mismatched at step %d", step)
	}
	m.results = append(m.results, result)
	if len(result.Content) == 0 || result.Content[0].Type != llm.ContentTypeText {
		return nil, errors.New("native tool metadata missing")
	}
	var observation desktop.Observation
	switch {
	case step == 1:
		var discovery desktop.Discovery
		if err := json.Unmarshal([]byte(result.Content[0].Text), &discovery); err != nil {
			return nil, err
		}
		for _, target := range m.targets {
			var selected desktop.Window
			for _, window := range discovery.Windows {
				if window.PID == target.pid && window.Title == target.name {
					selected = window
				}
			}
			if selected.Ref == "" {
				return nil, errors.New("exact synthetic window missing")
			}
			m.windows = append(m.windows, selected)
		}
	case (step-2)%3 == 0:
		if err := json.Unmarshal([]byte(result.Content[0].Text), &observation); err != nil {
			return nil, err
		}
	default:
		var action desktop.ActResult
		if err := json.Unmarshal([]byte(result.Content[0].Text), &action); err != nil {
			return nil, err
		}
		if !action.Dispatched || action.Outcome != "returned" || action.DriverError || action.ObservationError != "" || action.Observation == nil {
			return nil, errors.New("native action did not return a follow-up observation")
		}
		observation = *action.Observation
	}
	if step > 1 {
		index := (step - 2) / 3
		if observation.Ref == "" || observation.TargetRef != m.windows[index].Ref || observation.Degraded || observation.Complete {
			return nil, errors.New("invalid Linux actionable-only observation")
		}
		if len(result.Content) != 2 || result.Content[1].Type != llm.ContentTypeImage || result.Content[1].Image == nil {
			return nil, errors.New("native PNG did not reach the model")
		}
		img := result.Content[1].Image
		decoded, err := png.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil || img.MIMEType != "image/png" || decoded.Width != observation.ImageWidth || decoded.Height != observation.ImageHeight || decoded.Width <= 0 || decoded.Height <= 0 {
			return nil, errors.New("native PNG dimensions differ from observation")
		}
	}
	if step == 10 {
		return (&recordingModel{response: "Synthetic tool sequence finished."}).Stream(ctx, request)
	}
	index := (step - 1) / 3
	if (step-1)%3 == 0 {
		return call("desktop_observe", desktop.ObserveRequest{TargetRef: m.windows[index].Ref, Screenshot: true}), nil
	}
	kind, label := "set_value", "Task value"
	if (step-1)%3 == 2 {
		kind, label = "click", "Commit"
	}
	var token string
	for _, element := range observation.Elements {
		if element.Label == label {
			token = element.Token
		}
	}
	if token == "" {
		return nil, errors.New("actionable element missing")
	}
	act := desktop.ActRequest{Kind: kind, ObservationRef: observation.Ref, ElementToken: token, Screenshot: true}
	if kind == "set_value" {
		act.Text = linuxPrintValue(index)
	}
	return call("desktop_act", act), nil
}

func verifyLinuxPrintSession(t *testing.T, ctx context.Context, path string, results []llm.ToolResultMessage) {
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
	if len(snapshot.Messages) != 22 || len(snapshot.Compactions) != 0 {
		t.Fatal("unexpected durable history shape")
	}
	var parent string
	seen := make(map[string]bool)
	pending := make(map[string]string)
	index := 0
	for _, entry := range snapshot.Messages {
		if entry.ID == "" || seen[entry.ID] || entry.ParentID != parent {
			t.Fatal("broken durable message identity or parent")
		}
		seen[entry.ID], parent = true, entry.ID
		switch message := entry.Message.(type) {
		case llm.AssistantMessage:
			for _, part := range message.Content {
				if part.ToolCall != nil {
					pending[part.ToolCall.ID] = part.ToolCall.Name
				}
			}
		case llm.ToolResultMessage:
			if index >= len(results) || pending[message.ToolCallID] != message.ToolName || !reflect.DeepEqual(message, results[index]) {
				t.Fatal("replayed tool metadata/image differs from model input or call")
			}
			delete(pending, message.ToolCallID)
			index++
		}
	}
	if len(pending) != 0 || index != 10 || snapshot.LeafID != parent {
		t.Fatal("incomplete durable tool pairs")
	}
}
