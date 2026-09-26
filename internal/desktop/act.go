package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type ActRequest struct {
	Kind           string         `json:"action"`
	AppRef         string         `json:"app_ref,omitempty"`
	ObservationRef string         `json:"observation_ref"`
	ElementToken   string         `json:"element_token,omitempty"`
	Point          *Point         `json:"point,omitempty"`
	Text           string         `json:"text,omitempty"`
	Key            string         `json:"key,omitempty"`
	Keys           []string       `json:"keys,omitempty"`
	Direction      string         `json:"direction,omitempty"`
	Amount         int            `json:"amount,omitempty"`
	Wait           *WaitCondition `json:"wait,omitempty"`
	Screenshot     bool           `json:"screenshot"`
}

// ActResult separates dispatch/Driver response from the follow-up observation.
// An error after dispatch is data, not a Go error that a tool boundary could
// discard. A successful RPC by itself does not confirm a business postcondition.
type ActResult struct {
	Dispatched       bool            `json:"dispatched"`
	Outcome          string          `json:"outcome"`
	DriverError      bool            `json:"driver_error"`
	Driver           json.RawMessage `json:"driver,omitempty"`
	DriverText       []string        `json:"driver_text,omitempty"`
	Diagnostic       string          `json:"diagnostic,omitempty"`
	Observation      *Observation    `json:"observation,omitempty"`
	ObservationError string          `json:"observation_error,omitempty"`
	WaitState        string          `json:"wait_state,omitempty"`
	Windows          []Window        `json:"windows,omitempty"`
	WindowsTruncated bool            `json:"windows_truncated,omitempty"`
}

func (r *Run) Act(ctx context.Context, request ActRequest) (ActResult, error) {
	if request.Screenshot && !r.options.Images {
		return ActResult{}, errors.New("desktop: current model does not accept images")
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return ActResult{}, err
	}
	defer release()
	if request.Kind == "launch" {
		return r.launchLocked(ctx, request)
	}
	if request.AppRef != "" {
		return ActResult{}, errors.New("desktop: app_ref is only valid for launch")
	}
	binding, err := r.observationLocked(request.ObservationRef)
	if err != nil {
		return ActResult{}, err
	}
	if request.Kind == "wait" {
		return r.waitLocked(ctx, binding, request)
	}
	name, args, err := r.actionArguments(binding, request)
	if err != nil {
		return ActResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActResult{}, err
	}
	// A reference is consumed before dispatch, including when the Driver fails
	// to answer. It is never restored by observation failure or reconnection.
	delete(r.manager.latest, binding.target)
	delete(r.observations, request.ObservationRef)
	reply, err := r.callLocked(ctx, name, args)
	result := actionResult(reply, err)
	if err != nil {
		return result, nil
	}
	after, err := r.observeLocked(ctx, ObserveRequest{TargetRef: binding.targetRef, Screenshot: request.Screenshot})
	if err != nil {
		result.ObservationError = "Action response received, but follow-up observation failed; observe again before deciding what to do"
		return result, nil
	}
	result.Observation = &after
	return result, nil
}

func actionResult(reply Reply, err error) ActResult {
	result := ActResult{Dispatched: true, Outcome: "returned", DriverError: reply.IsError}
	if len(reply.Structured) <= 64*1024 {
		result.Driver = reply.Structured
	}
	remaining := 8192
	for _, text := range reply.Text {
		if remaining == 0 {
			break
		}
		result.DriverText = append(result.DriverText, boundedText(text, remaining))
		remaining -= min(len(text), remaining)
	}
	if err != nil {
		var before beforeDispatchError
		if errors.As(err, &before) {
			result.Dispatched = false
			result.Outcome = "not_dispatched"
			result.Diagnostic = before.Error()
			return result
		}
		result.Outcome = "unknown"
		result.Diagnostic = "Action was dispatched but no complete response was received. Do not repeat it without observing and checking the target."
		return result
	}
	if len(reply.Structured) > 64*1024 {
		result.Diagnostic = "Driver action details exceeded the result limit; inspect the fresh observation before continuing"
	}
	if reply.IsError && result.Diagnostic == "" {
		result.Diagnostic = "Driver reported an action error; partial effects may have occurred"
	}
	return result
}

func (r *Run) actionArguments(binding observationBinding, request ActRequest) (string, map[string]any, error) {
	if request.Wait != nil || (request.Key != "" && request.Kind != "key") || (len(request.Keys) != 0 && request.Kind != "hotkey") || ((request.Direction != "" || request.Amount != 0) && request.Kind != "scroll") || (request.Text != "" && request.Kind != "type_text" && request.Kind != "set_value") {
		return "", nil, errors.New("desktop: action contains unrelated fields")
	}
	windowKey := (request.Kind == "key" || request.Kind == "hotkey") && request.Point == nil && request.ElementToken == ""
	if !windowKey && (request.Point == nil) == (request.ElementToken == "") {
		return "", nil, errors.New("desktop: supply exactly one element token or screenshot point")
	}
	if len(request.Text) > 16*1024 {
		return "", nil, errors.New("desktop: text exceeds 16 KiB")
	}
	args := map[string]any{"session": r.id, "pid": binding.target.PID, "window_id": binding.target.WindowID}
	if request.ElementToken != "" {
		if _, ok := binding.tokens[request.ElementToken]; !ok {
			return "", nil, errors.New("desktop: element token does not belong to this observation")
		}
		args["element_token"] = request.ElementToken
	} else if request.Point != nil {
		x, y, err := binding.pixel(request.Point.X, request.Point.Y)
		if err != nil {
			return "", nil, err
		}
		args["x"], args["y"], args["capture_id"] = x, y, binding.capture
	}
	switch request.Kind {
	case "click", "double_click", "right_click":
		if request.Text != "" {
			return "", nil, errors.New("desktop: click does not accept text")
		}
		args["delivery_mode"] = "background"
		if request.Kind == "double_click" {
			if request.Point == nil {
				return "", nil, errors.New("desktop: double click requires a bound screenshot point")
			}
			args["count"] = 2
		}
		if request.Kind == "right_click" {
			args["button"] = "right"
		}
		return "click", args, nil
	case "type_text", "set_value":
		if request.Point != nil {
			return "", nil, errors.New("desktop: text requires an exact semantic element token")
		}
		if request.Kind == "type_text" {
			if request.Text == "" {
				return "", nil, errors.New("desktop: type_text requires text")
			}
			args["text"] = request.Text
			args["delivery_mode"] = "background"
		} else {
			args["value"] = request.Text
		}
		return request.Kind, args, nil
	case "key", "hotkey":
		if request.Point != nil {
			return "", nil, errors.New("desktop: key actions accept a semantic element or the observed window; click a bound pixel first if needed")
		}
		args["delivery_mode"] = "background"
		if request.Kind == "key" {
			if !validKey(request.Key) {
				return "", nil, errors.New("desktop: unsupported key name")
			}
			args["key"] = request.Key
			return "press_key", args, nil
		}
		if len(request.Keys) < 2 || len(request.Keys) > 6 || !validKey(request.Keys[len(request.Keys)-1]) {
			return "", nil, errors.New("desktop: hotkey requires modifiers followed by one key")
		}
		seen := make(map[string]bool)
		for _, key := range request.Keys[:len(request.Keys)-1] {
			if !validModifier(key) || seen[key] {
				return "", nil, errors.New("desktop: unsupported or duplicate hotkey modifier")
			}
			seen[key] = true
		}
		args["keys"] = request.Keys
		return "hotkey", args, nil
	case "scroll":
		if request.Point != nil {
			return "", nil, errors.New("desktop: scroll requires an exact semantic element token")
		}
		switch request.Direction {
		case "up", "down", "left", "right":
		default:
			return "", nil, errors.New("desktop: scroll direction must be up, down, left or right")
		}
		amount := request.Amount
		if amount == 0 {
			amount = 3
		}
		if amount < 1 || amount > 50 {
			return "", nil, errors.New("desktop: scroll amount must be 1..50")
		}
		args["direction"], args["amount"], args["by"], args["delivery_mode"] = request.Direction, amount, "line", "background"
		return "scroll", args, nil
	default:
		return "", nil, errors.New("desktop: unsupported typed action")
	}
}

func validModifier(key string) bool {
	switch key {
	case "cmd", "shift", "option", "ctrl", "fn":
		return true
	default:
		return false
	}
}

func validKey(key string) bool {
	if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9')) {
		return true
	}
	switch key {
	case "return", "tab", "escape", "up", "down", "left", "right", "space", "delete", "home", "end", "pageup", "pagedown":
		return true
	}
	if strings.HasPrefix(key, "f") {
		n, err := strconv.Atoi(key[1:])
		return err == nil && n >= 1 && n <= 12 && key == "f"+strconv.Itoa(n)
	}
	return false
}
