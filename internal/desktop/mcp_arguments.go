package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"unicode/utf8"
)

type managedArguments struct {
	PID          int      `json:"pid"`
	WindowID     uint64   `json:"window_id"`
	OnScreenOnly *bool    `json:"on_screen_only"`
	Screenshot   *bool    `json:"include_screenshot"`
	Query        string   `json:"query"`
	BundleID     string   `json:"bundle_id"`
	LaunchPath   string   `json:"launch_path"`
	ElementToken string   `json:"element_token"`
	X            *float64 `json:"x"`
	Y            *float64 `json:"y"`
	FromX        *float64 `json:"from_x"`
	FromY        *float64 `json:"from_y"`
	ToX          *float64 `json:"to_x"`
	ToY          *float64 `json:"to_y"`
	DurationMS   int      `json:"duration_ms"`
	Button       string   `json:"button"`
	Count        *int     `json:"count"`
	DeliveryMode string   `json:"delivery_mode"`
	Text         string   `json:"text"`
	Value        *string  `json:"value"`
	Key          string   `json:"key"`
	Keys         []string `json:"keys"`
	Direction    string   `json:"direction"`
	Amount       int      `json:"amount"`
}

func decodeManagedArguments(name, platform string, raw json.RawMessage) (managedArguments, error) {
	var args managedArguments
	fail := errors.New("desktop: invalid or unsupported managed CUA arguments")
	fields := managedFields(name, platform)
	if fields == nil || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return args, fail
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return args, fail
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(fields, key) {
			return args, fail
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return args, fail
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return args, fail
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return args, fail
	}
	if json.Unmarshal(raw, &args) != nil {
		return args, fail
	}
	if name != "list_apps" && name != "list_windows" && name != "launch_app" && (args.PID <= 0 || args.WindowID == 0) {
		return args, fail
	}
	if name == "list_windows" && seen["pid"] && args.PID <= 0 {
		return args, fail
	}
	if (args.X == nil) != (args.Y == nil) || len(args.Query) > 256 || len(args.ElementToken) > 512 {
		return args, fail
	}
	if name == "set_value" && args.Value == nil {
		return args, fail
	}
	if name == "drag" && (args.FromX == nil || args.FromY == nil || args.ToX == nil || args.ToY == nil) {
		return args, fail
	}
	if seen["duration_ms"] && (args.DurationMS < 1 || args.DurationMS > 10000) {
		return args, fail
	}
	if seen["amount"] && (args.Amount < 1 || args.Amount > 50) {
		return args, fail
	}
	if name == "launch_app" && ((platform == "linux" && (args.LaunchPath == "" || len(args.LaunchPath) > 16*1024)) || (platform != "linux" && (args.BundleID == "" || len(args.BundleID) > 512))) {
		return args, fail
	}
	return args, nil
}

func (a managedArguments) action(name string) (ActRequest, error) {
	request := ActRequest{Kind: name, ElementToken: a.ElementToken, DeliveryMode: a.DeliveryMode, Text: a.Text, Key: a.Key, Keys: a.Keys, Direction: a.Direction, Amount: a.Amount}
	if a.X != nil {
		request.Point = &Point{X: *a.X, Y: *a.Y}
	}
	switch name {
	case "click":
		count := 1
		if a.Count != nil {
			count = *a.Count
		}
		if count < 1 || count > 2 || (a.Button != "" && a.Button != "left" && a.Button != "right") || (a.Button == "right" && count != 1) {
			return ActRequest{}, errors.New("desktop: managed click supports left single/double or right single click")
		}
		if a.Button == "right" {
			request.Kind = "right_click"
		} else if count == 2 {
			request.Kind = "double_click"
		}
	case "press_key":
		request.Kind = "key"
	case "set_value":
		request.Text = *a.Value
	case "drag":
		request.Drag = &DragGesture{From: &Point{X: *a.FromX, Y: *a.FromY}, To: &Point{X: *a.ToX, Y: *a.ToY}, DurationMS: a.DurationMS}
	}
	return request, nil
}
