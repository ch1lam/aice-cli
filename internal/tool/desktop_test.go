package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type fakeDesktopBackend struct {
	apps    func(context.Context, string, int) (desktop.Discovery, error)
	observe func(context.Context, desktop.ObserveRequest) (desktop.Observation, error)
	act     func(context.Context, desktop.ActRequest) (desktop.ActResult, error)
}

func (f fakeDesktopBackend) Apps(ctx context.Context, q string, n int) (desktop.Discovery, error) {
	return f.apps(ctx, q, n)
}
func (f fakeDesktopBackend) Observe(ctx context.Context, r desktop.ObserveRequest) (desktop.Observation, error) {
	return f.observe(ctx, r)
}
func (f fakeDesktopBackend) Act(ctx context.Context, r desktop.ActRequest) (desktop.ActResult, error) {
	return f.act(ctx, r)
}

func TestDesktopToolPreservesImageAndPartialAction(t *testing.T) {
	t.Parallel()
	img := &llm.ImageContent{Data: []byte("synthetic-view"), MIMEType: "image/png", Original: &llm.ImageOriginal{Data: []byte("synthetic-original"), MIMEType: "image/png"}}
	observation := desktop.Observation{Ref: "fresh", Image: img}
	backend := fakeDesktopBackend{
		observe: func(context.Context, desktop.ObserveRequest) (desktop.Observation, error) { return observation, nil },
		act: func(context.Context, desktop.ActRequest) (desktop.ActResult, error) {
			return desktop.ActResult{Dispatched: true, Outcome: "unknown", Observation: &observation, Driver: json.RawMessage(`{broken`)}, errors.New("response lost")
		},
	}
	tools, err := NewDesktopTools(backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 2} {
		tool := tools[index]
		call := llm.ToolCall{ID: "call", Name: tool.Definition().Name, Arguments: json.RawMessage(`{}`)}
		result, err := tool.Execute(t.Context(), call)
		if err != nil || result.CallID != call.ID || len(result.Content) != 2 {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		if result.Content[1].Type != llm.ContentTypeImage || result.Content[1].Image != img {
			t.Fatal("image lost or rendered as text")
		}
		text := result.Content[0].Text
		if strings.Contains(text, "synthetic") || strings.Contains(text, "c3ludGhldGlj") {
			t.Fatal("image bytes leaked into text")
		}
		if index == 2 && (!result.IsError || !strings.Contains(text, `"dispatched":true`) || !strings.Contains(text, `"outcome":"unknown"`) || !strings.Contains(text, "response lost")) {
			t.Fatalf("partial action facts lost: %+v", result)
		}
	}
}

func TestDesktopToolStrictArgumentsAndSetupError(t *testing.T) {
	t.Parallel()
	calls := 0
	tools, err := NewDesktopTools(fakeDesktopBackend{apps: func(_ context.Context, query string, limit int) (desktop.Discovery, error) {
		calls++
		if query != "" || limit != 16 {
			t.Errorf("query=%q limit=%d", query, limit)
		}
		return desktop.Discovery{}, &desktop.ServiceError{Code: "setup_required", Detail: "Open Settings"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"permission_mode":"off"}`, `{"limit":"all"}`, `{} {}`} {
		if _, err := tools[0].Execute(t.Context(), llm.ToolCall{Name: "desktop_apps", Arguments: []byte(args)}); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid arguments reached backend")
	}
	result, err := tools[0].Execute(t.Context(), llm.ToolCall{ID: "c", Name: "desktop_apps", Arguments: []byte(`{}`)})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].Text, `"code":"setup_required"`) || calls != 1 {
		t.Fatalf("setup result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestDesktopToolWaitAndObservationFailureRemainExplicit(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"satisfied", "unsatisfied", "unknown"} {
		t.Run(state, func(t *testing.T) {
			tools, _ := NewDesktopTools(fakeDesktopBackend{act: func(context.Context, desktop.ActRequest) (desktop.ActResult, error) {
				return desktop.ActResult{Outcome: "returned", WaitState: state}, nil
			}})
			result, err := tools[2].Execute(t.Context(), llm.ToolCall{Name: "desktop_act", Arguments: []byte(`{"action":"wait"}`)})
			if err != nil || result.IsError != (state != "satisfied") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestDesktopToolCarriesExplicitForegroundChoiceAndFreshOpportunity(t *testing.T) {
	t.Parallel()
	calls := 0
	tools, err := NewDesktopTools(fakeDesktopBackend{act: func(_ context.Context, request desktop.ActRequest) (desktop.ActResult, error) {
		calls++
		if calls == 1 {
			if request.DeliveryMode != "" {
				t.Fatal("tool changed default delivery", request)
			}
			return desktop.ActResult{Dispatched: true, Outcome: "returned", DriverError: true,
				Observation: &desktop.Observation{Ref: "fresh", ForegroundAction: "hotkey"}}, nil
		}
		if request.DeliveryMode != "foreground" || request.ObservationRef != "fresh" || request.Kind != "hotkey" {
			t.Fatal("foreground choice lost at tool boundary", request)
		}
		return desktop.ActResult{Dispatched: true, Outcome: "returned", Observation: &desktop.Observation{Ref: "new"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools[2].Execute(t.Context(), llm.ToolCall{Name: "desktop_act", Arguments: []byte(`{"action":"hotkey","observation_ref":"original","keys":["cmd","s"]}`)})
	if err != nil || !result.IsError || calls != 1 || !strings.Contains(result.Content[0].Text, `"foreground_action_available":"hotkey"`) {
		t.Fatal("refusal lost its next-decision evidence", result, err)
	}
	result, err = tools[2].Execute(t.Context(), llm.ToolCall{Name: "desktop_act", Arguments: []byte(`{"action":"hotkey","observation_ref":"fresh","keys":["cmd","s"],"delivery_mode":"foreground"}`)})
	if err != nil || result.IsError || calls != 2 || strings.Contains(result.Content[0].Text, `foreground_action_available`) {
		t.Fatal("explicit choice did not complete once", result, err)
	}
}
