package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// A missing axis must not silently become coordinate zero. Keep zero itself
// valid when it was explicitly supplied, and reject unknown nested fields.
func (p *Point) UnmarshalJSON(data []byte) error {
	var wire struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.X == nil || wire.Y == nil {
		return errors.New("desktop: point requires explicit numeric x and y")
	}
	p.X, p.Y = *wire.X, *wire.Y
	return nil
}

type actionRequest struct {
	Kind         string       `json:"action"`
	DeliveryMode string       `json:"delivery_mode,omitempty"`
	ElementToken string       `json:"element_token,omitempty"`
	Point        *Point       `json:"point,omitempty"`
	Drag         *DragGesture `json:"drag,omitempty"`
	Text         string       `json:"text,omitempty"`
	Key          string       `json:"key,omitempty"`
	Keys         []string     `json:"keys,omitempty"`
	Direction    string       `json:"direction,omitempty"`
	Amount       int          `json:"amount,omitempty"`
}

func (r *Run) actionArguments(binding observationBinding, request actionRequest) (string, map[string]any, error) {
	delivery, err := r.actionDelivery(binding, request)
	if err != nil {
		return "", nil, err
	}
	if (request.Drag != nil && request.Kind != "drag") || (request.Key != "" && request.Kind != "key") || (len(request.Keys) != 0 && request.Kind != "hotkey") || ((request.Direction != "" || request.Amount != 0) && request.Kind != "scroll") || (request.Text != "" && request.Kind != "type_text" && request.Kind != "set_value") {
		return "", nil, errors.New("desktop: action contains unrelated fields")
	}
	if request.Kind == "drag" {
		return r.dragArguments(binding, request, delivery)
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
		args["x"], args["y"] = x, y
		switch request.Kind {
		case "click", "double_click", "right_click":
			args["capture_id"] = binding.capture
		case "scroll", "type_text":
			if binding.snapshot == "" {
				return "", nil, errors.New("desktop: pixel input requires a current session-owned screenshot snapshot")
			}
		}
	}
	switch request.Kind {
	case "click", "double_click", "right_click":
		if request.Text != "" {
			return "", nil, errors.New("desktop: click does not accept text")
		}
		args["delivery_mode"] = delivery
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
		if request.Kind == "set_value" && request.Point != nil {
			return "", nil, errors.New("desktop: set_value requires an exact semantic element token")
		}
		if request.Kind == "type_text" {
			if request.Text == "" {
				return "", nil, errors.New("desktop: type_text requires text")
			}
			args["text"] = request.Text
			args["delivery_mode"] = delivery
		} else {
			args["value"] = request.Text
		}
		return request.Kind, args, nil
	case "key", "hotkey":
		if request.Point != nil {
			return "", nil, errors.New("desktop: key actions accept a semantic element or the observed window; click a bound pixel first if needed")
		}
		args["delivery_mode"] = delivery
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
		args["direction"], args["amount"], args["by"], args["delivery_mode"] = request.Direction, amount, "line", delivery
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
