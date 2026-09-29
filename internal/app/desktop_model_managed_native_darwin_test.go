//go:build integration && darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func TestNativeMacManagedModelHarness(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 for the managed model harness; no provider access")
	}
	model, options, err := resolveModelSettings(defaultProviders(), config.Config{Provider: "custom", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	testNativeMacModelTask(t, model, options, func(targets []nativePrintFixture) llm.Streamer {
		return &nativeManagedCUAModel{targets: targets, names: make(map[string]string), inputActions: map[int]string{1: "type_text"}}
	}, false, 100000, 80)
}

// Fixture-only scope: each PID is a process started by this test. Discovery
// requires that PID; alternative targets and native file/launch options cannot
// escape this synthetic task. Driver owns snapshot/token validity. This wrapper
// neither rewrites native results nor replaces the final dispatch check.
func (s *nativeModelScope) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	refuse := func() (mcpclient.Result, error) {
		return mcpclient.Result{State: llm.ExecutionNotDispatched, IsError: true}, s.refuse()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return refuse()
	}
	for field := range fields {
		switch field {
		case "pid", "window_id", "delivery_mode", "include_screenshot", "query", "max_elements", "max_depth", "max_image_dimension", "timeout_ms", "on_screen_only", "element_token", "element_index", "snapshot_id", "capture_id", "x", "y", "key", "keys", "text", "value":
		default:
			return refuse()
		}
	}
	var args struct {
		PID          int      `json:"pid"`
		Window       uint64   `json:"window_id"`
		DeliveryMode string   `json:"delivery_mode"`
		Key          string   `json:"key"`
		Keys         []string `json:"keys"`
	}
	if json.Unmarshal(raw, &args) != nil || s.managedDesktopRun == nil || !slices.ContainsFunc(s.targets, func(target nativePrintFixture) bool { return target.pid > 0 && target.pid == args.PID }) || args.DeliveryMode != "" && args.DeliveryMode != "background" {
		return refuse()
	}
	switch name {
	case "list_windows":
	case "get_window_state", "click", "type_text", "set_value":
		if args.Window == 0 {
			return refuse()
		}
	case "hotkey":
		if args.Window == 0 || !slices.Equal(args.Keys, []string{"cmd", "a"}) {
			return refuse()
		}
	case "press_key":
		if args.Window == 0 || args.Key != "delete" {
			return refuse()
		}
	default:
		return refuse()
	}
	started := time.Now()
	result, err := s.managedDesktopRun.CallChecked(ctx, name, raw, check)
	if s.timings != nil && s.timings.active != nil {
		elapsed := nativeMS(time.Since(started))
		s.timings.active.ManagedCallMS = &elapsed
		s.timings.active.Name = name
	}
	if err == nil && !result.IsError && result.State == llm.ExecutionReturned && name == "get_window_state" {
		for _, block := range result.Content {
			if block.Kind == mcpclient.BlockImage && len(block.Data) > 0 {
				s.captured[fmt.Sprint(args.PID)] = true
			}
		}
	}
	return result, err
}

func TestNativeManagedModelScope(t *testing.T) {
	t.Parallel()
	backend := &nativeModelManagedFixture{result: mcpclient.Result{State: llm.ExecutionUnknown, Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "unknown action"}}}}
	scope := &nativeModelScope{managedDesktopRun: backend, targets: []nativePrintFixture{{pid: 41}}, captured: make(map[string]bool)}
	for _, tc := range []struct{ name, args string }{
		{"list_apps", `{}`},
		{"list_windows", `{}`},
		{"list_windows", `{"pid":42}`},
		{"get_window_state", `{"pid":41}`},
		{"click", `{"pid":41,"window_id":9,"delivery_mode":"foreground"}`},
		{"launch_app", `{"pid":41}`},
		{"hotkey", `{"pid":41,"window_id":9,"keys":["cmd","q"]}`},
		{"press_key", `{"pid":41,"window_id":9,"key":"return"}`},
		{"click", `{"pid":41,"window_id":9,"target":{"kind":"window","pid":42,"window_id":10}}`},
		{"click", `{"pid":41,"window_id":9,"scope":"desktop"}`},
		{"get_window_state", `{"pid":41,"window_id":9,"screenshot_out_file":"/tmp/out.png"}`},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			result, err := scope.CallChecked(t.Context(), tc.name, []byte(tc.args), nil)
			if err == nil || result.State != llm.ExecutionNotDispatched || backend.calls != 0 {
				t.Fatal("scope refusal reached native backend")
			}
		})
	}
	denied := errors.New("revoked at dispatch")
	result, err := scope.CallChecked(t.Context(), "click", []byte(`{"pid":41,"window_id":9}`), func(context.Context) error { return denied })
	if !errors.Is(err, denied) || result.State != llm.ExecutionNotDispatched || backend.calls != 0 {
		t.Fatal("scope replaced final native authorization check")
	}
	result, err = scope.CallChecked(t.Context(), "click", []byte(`{"pid":41,"window_id":9}`), nil)
	if err != nil || backend.calls != 1 || !reflect.DeepEqual(result, backend.result) {
		t.Fatal("scope rewrote or retried native unknown result")
	}
}

type nativeModelManagedFixture struct {
	managedDesktopRun
	calls  int
	result mcpclient.Result
}

func (f *nativeModelManagedFixture) CallChecked(ctx context.Context, _ string, _ json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if check != nil {
		if err := check(ctx); err != nil {
			return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
		}
	}
	f.calls++
	return f.result, nil
}
