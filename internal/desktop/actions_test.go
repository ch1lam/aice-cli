package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestKeyHotkeyAndScrollKeepExactWindowAndBackground(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"key", "hotkey", "scroll"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name != "press_key" && name != "hotkey" && name != "scroll" {
					return Reply{}, nil, false
				}
				if args["pid"] != 41 || args["window_id"] != uint64(99) || args["delivery_mode"] != "background" || args["scope"] == "desktop" {
					t.Fatal("input escaped exact background window", args)
				}
				if name == "press_key" && args["key"] != "return" {
					t.Fatal(args)
				}
				if name == "hotkey" && fmt.Sprint(args["keys"]) != "[cmd s]" {
					t.Fatal(args)
				}
				if name == "scroll" && (args["element_token"] == "" || args["direction"] != "down" || args["amount"] != 3) {
					t.Fatal(args)
				}
				return structuredReply(map[string]any{"effect": "unverifiable"}), nil, true
			}}
			_, r := testRun(t, f, false)
			o := observed(t, r, false)
			request := ActRequest{Kind: kind, ObservationRef: o.Ref}
			switch kind {
			case "key":
				request.Key = "return"
			case "hotkey":
				request.Keys = []string{"cmd", "s"}
			case "scroll":
				request.ElementToken, request.Direction = o.Elements[0].Token, "down"
			}
			result, err := r.Act(t.Context(), request)
			if err != nil || !result.Dispatched || result.Outcome != "returned" || result.Observation == nil || f.count("get_window_state") != 2 {
				t.Fatal("action/observation sequence failed", result, err)
			}
			if _, err := r.Act(t.Context(), request); err == nil {
				t.Fatal("consumed action repeated")
			}
		})
	}
}

func TestTypedActionsRejectMixedOrInvalidArgumentsBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, request := range []ActRequest{
		{Kind: "key", Key: "return", Text: "ignored"},
		{Kind: "key", Key: "cmd+s"},
		{Kind: "key", Key: "f99"},
		{Kind: "key", Key: "return", Point: &Point{}},
		{Kind: "hotkey", Keys: []string{"s", "cmd"}},
		{Kind: "hotkey", Keys: []string{"cmd", "cmd", "s"}},
		{Kind: "hotkey", Keys: []string{"cmd"}},
		{Kind: "scroll", Direction: "down", Amount: 51, ElementToken: "current"},
		{Kind: "scroll", Direction: "diagonal", ElementToken: "current"},
		{Kind: "click", Key: "return", ElementToken: "current"},
		{Kind: "set_value", Direction: "down", ElementToken: "current"},
		{Kind: "wait", Wait: &WaitCondition{Text: "done", TimeoutMS: 50}, Key: "return"},
		{Kind: "wait", Wait: &WaitCondition{Text: strings.Repeat("x", 257), TimeoutMS: 50}},
		{Kind: "wait", Wait: &WaitCondition{Text: "done", TimeoutMS: 10001}},
	} {
		t.Run(fmt.Sprintf("%+v", request), func(t *testing.T) {
			f := &fakeDriver{}
			_, r := testRun(t, f, false)
			o := observed(t, r, false)
			request.ObservationRef = o.Ref
			if request.ElementToken == "current" {
				request.ElementToken = o.Elements[0].Token
			}
			before := len(f.calls)
			if _, err := r.Act(t.Context(), request); err == nil {
				t.Fatal("invalid action accepted")
			}
			if len(f.calls) != before {
				t.Fatal("invalid action dispatched")
			}
			// Rejected local validation does not consume a valid observation.
			if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err != nil {
				t.Fatal("validation consumed reference", err)
			}
		})
	}
}

func TestSemanticWaitUsesFreshObservationsAndCapturesOnlyAtCompletion(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
		if name != "get_window_state" || f.count(name) < 3 {
			return Reply{}, nil, false
		}
		return structuredReply(map[string]any{
			"pid": args["pid"], "window_id": args["window_id"], "snapshot_id": fmt.Sprint(f.count(name)),
			"elements": []any{map[string]any{"element_token": "done-token", "value": "saved successfully"}},
		}), nil, true
	}
	_, r := testRun(t, f, true)
	o := observed(t, r, false)
	result, err := r.Act(t.Context(), ActRequest{Kind: "wait", ObservationRef: o.Ref, Wait: &WaitCondition{Text: "saved", TimeoutMS: 1500}, Screenshot: true})
	if err != nil || result.Dispatched || result.WaitState != "satisfied" || result.Observation == nil || f.count("get_window_state") != 4 {
		t.Fatal(result, err, f.count("get_window_state"))
	}
	images := 0
	for _, call := range f.calls {
		if call.args["include_screenshot"] == true {
			images++
		}
	}
	if images != 1 {
		t.Fatal("wait captured per polling tick", images)
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("wait retained old execution reference")
	}
}

func TestWaitMissingTextPreservesUnknownForIncompleteObservation(t *testing.T) {
	t.Parallel()
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name != "get_window_state" {
					return Reply{}, nil, false
				}
				return structuredReply(map[string]any{"pid": args["pid"], "window_id": args["window_id"], "elements_complete": complete}), nil, true
			}}
			_, r := testRun(t, f, false)
			o := observed(t, r, false)
			result, err := r.Act(t.Context(), ActRequest{Kind: "wait", ObservationRef: o.Ref, Wait: &WaitCondition{Text: "absent", TimeoutMS: 25}})
			want := "unknown"
			if complete {
				want = "unsatisfied"
			}
			if err != nil || result.WaitState != want || result.Observation == nil {
				t.Fatal(result, err)
			}
		})
	}
}

func TestWaitDeadlineCancelsAnInFlightObservation(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, r := testRun(t, f, false)
	o := observed(t, r, false)
	f.handle = func(ctx context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name != "get_window_state" {
			return Reply{}, nil, false
		}
		<-ctx.Done()
		return Reply{}, ctx.Err(), true
	}
	result, err := r.Act(t.Context(), ActRequest{Kind: "wait", ObservationRef: o.Ref, Wait: &WaitCondition{Text: "ready", TimeoutMS: 25}})
	if err != nil || result.WaitState != "unknown" || result.Observation != nil || result.ObservationError == "" || f.count("get_window_state") != 2 {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Act(ctx, ActRequest{Kind: "key", ObservationRef: o.Ref, Key: "return"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled wait could be followed by input", err)
	}
}
