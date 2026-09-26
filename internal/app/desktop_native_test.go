//go:build integration && (darwin || linux)

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

type nativePrintFixture struct {
	directory, name string
	pid             int
}
type nativePrintState struct {
	PID          int    `json:"pid"`
	Ticks        int    `json:"ticks"`
	Armed        bool   `json:"armed"`
	FrontIsLogin bool   `json:"front_is_login"`
	Active       bool   `json:"active"`
	FocusLosses  int    `json:"focus_losses"`
	KeysSent     int    `json:"keys_sent"`
	Commits      int    `json:"commits"`
	Value        string `json:"value"`
	Result       string `json:"result"`
}

func writeNativePrintSignal(t *testing.T, fixture nativePrintFixture, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.directory, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitNativePrintState(t *testing.T, ctx context.Context, fixture nativePrintFixture, check func(nativePrintState) bool) nativePrintState {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(fixture.directory, "state.json"))
		if err == nil {
			var state nativePrintState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.FrontIsLogin {
				t.Fatal("native fixture requires an available desktop; loginwindow is foreground (no unlock attempted)")
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
type nativePrintModel struct {
	t        *testing.T
	query    string
	targets  []nativePrintFixture
	windows  []desktop.Window
	requests int
	results  []llm.ToolResultMessage
}

func nativePrintValue(index int) string { return fmt.Sprintf("AICE CLI stage %d 中文 ✓", index+1) }

func (m *nativePrintModel) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
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
		return call("desktop_apps", map[string]any{"query": m.query, "limit": 16}), nil
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
		if observation.Ref == "" || observation.TargetRef != m.windows[index].Ref || observation.Degraded {
			return nil, errors.New("invalid native observation")
		}
		if runtime.GOOS == "linux" && observation.Complete {
			return nil, errors.New("Linux actionable-only projection claimed completeness")
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
		act.Text = nativePrintValue(index)
	}
	return call("desktop_act", act), nil
}

func verifyNativePrintSession(t *testing.T, ctx context.Context, path string, results []llm.ToolResultMessage) {
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
