package desktop

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

func backgroundRefusal() Reply {
	reply := structuredReply(map[string]any{"code": "background_unavailable", "effect": "refused", "pid": 41, "window_id": 99})
	reply.IsError = true
	return reply
}

func TestForegroundRequiresRefusalThenAnExplicitSameAction(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
		if name != "type_text" {
			return Reply{}, nil, false
		}
		if args["pid"] != 41 || args["window_id"] != uint64(99) || args["scope"] != nil {
			t.Fatal("foreground escaped the exact window", args)
		}
		if args["delivery_mode"] == "background" {
			return backgroundRefusal(), nil, true
		}
		if args["delivery_mode"] != "foreground" || args["element_token"] != "opaque-token-2" {
			t.Fatal("foreground did not use the new target", args)
		}
		return structuredReply(map[string]any{"effect": "unverifiable"}), nil, true
	}}
	_, r := testRun(t, f, false)
	r.options.Mode = ForegroundAllowed
	o := observed(t, r, false)
	request := ActRequest{Kind: "type_text", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token, Text: "synthetic", DeliveryMode: "foreground"}
	if _, err := r.Act(t.Context(), request); err == nil || f.count("type_text") != 0 {
		t.Fatal("foreground ran without a background refusal", err)
	}
	request.DeliveryMode = ""
	result, err := r.Act(t.Context(), request)
	if err != nil || result.Observation == nil || result.Observation.ForegroundAction != "type_text" || !result.DriverError || f.count("type_text") != 1 {
		t.Fatal("refusal was lost or automatically replayed", result, err)
	}
	request.DeliveryMode = "foreground"
	if _, err := r.Act(t.Context(), request); err == nil {
		t.Fatal("refused observation survived")
	}
	request.ObservationRef = result.Observation.Ref
	if _, err := r.Act(t.Context(), request); err == nil {
		t.Fatal("old element survived refresh")
	}
	request.ElementToken = result.Observation.Elements[0].Token
	changed := request
	changed.Text = "different action"
	if _, err := r.Act(t.Context(), changed); err == nil || f.count("type_text") != 1 {
		t.Fatal("opportunity permitted different input", err)
	}
	result, err = r.Act(t.Context(), request)
	if err != nil || result.Observation == nil || result.Observation.ForegroundAction != "" || f.count("type_text") != 2 {
		t.Fatal("explicit foreground action failed or renewed opportunity", result, err)
	}
	if _, err := r.Act(t.Context(), request); err == nil {
		t.Fatal("foreground action replayed")
	}
}

func TestForegroundOpportunityExpiresAndNeverOverridesMode(t *testing.T) {
	t.Parallel()
	for _, expiry := range []string{"background_only", "observe", "other_run", "disconnect", "cancel", "post_observation_failure", "lost_response"} {
		t.Run(expiry, func(t *testing.T) {
			f := &fakeDriver{handle: func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
				if name == "type_text" {
					if expiry == "lost_response" {
						return backgroundRefusal(), io.EOF, true
					}
					return backgroundRefusal(), nil, true
				}
				return Reply{}, nil, false
			}}
			m, r := testRun(t, f, false)
			if expiry != "background_only" {
				r.options.Mode = ForegroundAllowed
			}
			o := observed(t, r, false)
			if expiry == "post_observation_failure" {
				f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
					if name == "get_window_state" {
						return Reply{}, io.EOF, true
					}
					if name == "type_text" {
						return backgroundRefusal(), nil, true
					}
					return Reply{}, nil, false
				}
			}
			request := ActRequest{Kind: "type_text", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token, Text: "synthetic"}
			result, err := r.Act(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Observation != nil {
				o = *result.Observation
			} else if expiry != "lost_response" && expiry != "post_observation_failure" {
				t.Fatal("unexpected missing observation", result)
			}
			switch expiry {
			case "background_only":
				if o.ForegroundAction != "" {
					t.Fatal("background-only mode offered foreground")
				}
			case "observe":
				o, err = r.Observe(t.Context(), ObserveRequest{TargetRef: o.TargetRef})
				if err != nil || o.ForegroundAction != "" {
					t.Fatal("ordinary refresh retained opportunity", o, err)
				}
			case "other_run":
				other, err := m.Bind(t.Context(), RunOptions{Mode: ForegroundAllowed})
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				_ = observed(t, other, false)
			case "disconnect":
				_ = m.Disconnect(t.Context())
			case "cancel":
				_ = r.Close()
			}
			request.ObservationRef, request.ElementToken = o.Ref, o.Elements[0].Token
			request.DeliveryMode = "foreground"
			if _, err := r.Act(t.Context(), request); err == nil || f.count("type_text") != 1 {
				t.Fatal("expired opportunity dispatched input", err)
			}
		})
	}
}

func TestForegroundRefusalClassifierRequiresReviewedPreInputPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, token, body string
		isError, want           bool
	}{
		{"electron", "type_text", "token", `{"code":"background_unavailable","effect":"refused","pid":41,"window_id":99}`, true, true},
		{"screen_text", "type_text", "token", `{"code":"SCREEN_SHARING_REQUIRES_FOREGROUND_HID","effect":"refused"}`, true, true},
		{"screen_hotkey", "hotkey", "", `{"code":"SCREEN_SHARING_REQUIRES_FOREGROUND_HID","effect":"refused"}`, true, true},
		{"window_key", "key", "", `{"code":"same_pid_keyboard_ambiguity","effect":"refused","pid":41,"window_id":99}`, true, true},
		{"window_hotkey", "hotkey", "", `{"code":"same_pid_keyboard_ambiguity","effect":"refused","pid":41,"window_id":99}`, true, true},
		{"element_key", "key", "token", `{"code":"same_pid_keyboard_ambiguity","effect":"refused","pid":41,"window_id":99}`, true, false},
		{"partial", "type_text", "token", `{"code":"background_unavailable","effect":"partial","pid":41,"window_id":99}`, true, false},
		{"unverifiable", "type_text", "token", `{"code":"background_unavailable","effect":"unverifiable","pid":41,"window_id":99}`, true, false},
		{"advice_only", "type_text", "token", `{"escalation":{"recommended":"foreground"}}`, true, false},
		{"not_error", "type_text", "token", `{"code":"background_unavailable","effect":"refused","pid":41,"window_id":99}`, false, false},
		{"foreign", "type_text", "token", `{"code":"background_unavailable","effect":"refused","pid":42,"window_id":99}`, true, false},
		{"missing_identity", "type_text", "token", `{"code":"background_unavailable","effect":"refused"}`, true, false},
		{"unknown_code", "type_text", "token", `{"code":"stale_target","effect":"refused","pid":41,"window_id":99}`, true, false},
		{"scroll", "scroll", "token", `{"code":"background_unavailable","effect":"refused","pid":41,"window_id":99}`, true, false},
		{"click", "click", "token", `{"code":"background_unavailable","effect":"refused","pid":41,"window_id":99}`, true, false},
		{"malformed", "type_text", "token", `{`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := safeForegroundRefusal(observationBinding{target: windowIdentity{PID: 41, WindowID: 99}}, ActRequest{Kind: tc.kind, ElementToken: tc.token}, Reply{IsError: tc.isError, Structured: json.RawMessage(tc.body)})
			if got != tc.want {
				t.Fatalf("eligible=%v, want %v", got, tc.want)
			}
		})
	}
}
