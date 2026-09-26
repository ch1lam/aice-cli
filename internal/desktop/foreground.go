package desktop

import (
	"encoding/json"
	"errors"
)

func (r *Run) actionDelivery(binding observationBinding, request ActRequest) (string, error) {
	if request.Kind == "set_value" && request.DeliveryMode != "" {
		return "", errors.New("desktop: set_value does not accept delivery_mode")
	}
	switch request.DeliveryMode {
	case "", "background":
		return "background", nil
	case "foreground":
		if r.options.Mode != ForegroundAllowed {
			return "", errors.New("desktop: this run permits background input only")
		}
		if binding.foregroundAction == "" || binding.foregroundAction != foregroundActionKey(request) {
			return "", errors.New("desktop: foreground requires the fresh observation returned by a verified pre-input background refusal, with the same action and target form")
		}
		return "foreground", nil
	default:
		return "", errors.New("desktop: delivery_mode must be background or foreground")
	}
}

// Keep every action field in the comparison, including future additions. Only
// observation-local tokens, re-grounded image coordinates and presentation
// choices may change. The Agent must identify the intended target again in the
// returned observation; references from the refused action cannot survive it.
func foregroundActionKey(request ActRequest) string {
	request.ObservationRef, request.DeliveryMode = "", ""
	request.Screenshot = false
	if request.ElementToken != "" {
		request.ElementToken = "semantic"
	}
	if request.Point != nil {
		request.Point = &Point{}
	}
	if request.Drag != nil {
		gesture := *request.Drag
		gesture.From, gesture.To = &Point{}, &Point{}
		request.Drag = &gesture
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// These are specific macOS 0.29.1 paths reviewed before their first actuator:
// type_text.rs screen_sharing_delivery_error/electron_background_ax_refusal,
// hotkey.rs screen_sharing_modifier_delivery_error, and the initial GenericKey
// gate in press_key.rs/hotkey.rs for window-only requests. Generic effect=refused
// is insufficient: other paths can refuse after focus or partial input.
// scroll.rs (Electron) and drag.rs also have early background_unavailable
// returns with no effect field, before resolving targets or invoking input.
// Only the macOS adapter uses this classifier. Linux refusals need their own
// pre-input evidence before a foreground continuation can be offered.
func safeForegroundRefusal(binding observationBinding, request ActRequest, reply Reply) bool {
	if !reply.IsError || len(reply.Structured) > 64*1024 {
		return false
	}
	var refusal struct {
		Code     string  `json:"code"`
		Effect   string  `json:"effect"`
		PID      *int    `json:"pid"`
		WindowID *uint64 `json:"window_id"`
	}
	if json.Unmarshal(reply.Structured, &refusal) != nil {
		return false
	}
	if (refusal.PID != nil && *refusal.PID != binding.target.PID) || (refusal.WindowID != nil && *refusal.WindowID != binding.target.WindowID) {
		return false
	}
	if refusal.Code == "background_unavailable" && refusal.Effect == "" && refusal.PID == nil && refusal.WindowID == nil {
		// These two pinned paths emit exactly the code-only object. Do not
		// treat an explicit null effect or unreviewed extra outcome as refusal.
		var fields map[string]json.RawMessage
		if json.Unmarshal(reply.Structured, &fields) != nil || len(fields) != 1 {
			return false
		}
		return request.Kind == "scroll" || request.Kind == "drag"
	}
	if refusal.Effect != "refused" {
		return false
	}
	if refusal.Code == "SCREEN_SHARING_REQUIRES_FOREGROUND_HID" {
		return request.Kind == "type_text" || request.Kind == "hotkey"
	}
	if refusal.PID == nil || refusal.WindowID == nil {
		return false
	}
	switch refusal.Code {
	case "background_unavailable":
		return request.Kind == "type_text" && request.ElementToken != ""
	case "same_pid_keyboard_ambiguity":
		return (request.Kind == "key" || request.Kind == "hotkey") && request.ElementToken == ""
	default:
		return false
	}
}
