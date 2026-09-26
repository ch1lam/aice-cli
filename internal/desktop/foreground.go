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
// observation-local tokens and presentation choices may change. The Agent must
// identify the intended element again in the returned observation; tokens from
// the refused action cannot survive a refresh.
func foregroundActionKey(request ActRequest) string {
	request.ObservationRef, request.DeliveryMode = "", ""
	request.Screenshot = false
	if request.ElementToken != "" {
		request.ElementToken = "semantic"
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
// Admission currently permits only the pinned macOS service. Re-review this
// classifier before admitting another version or platform.
func safeForegroundRefusal(binding observationBinding, request ActRequest, reply Reply) bool {
	if !reply.IsError || request.Point != nil || len(reply.Structured) > 64*1024 {
		return false
	}
	var refusal struct {
		Code     string  `json:"code"`
		Effect   string  `json:"effect"`
		PID      *int    `json:"pid"`
		WindowID *uint64 `json:"window_id"`
	}
	if json.Unmarshal(reply.Structured, &refusal) != nil || refusal.Effect != "refused" {
		return false
	}
	if (refusal.PID != nil && *refusal.PID != binding.target.PID) || (refusal.WindowID != nil && *refusal.WindowID != binding.target.WindowID) {
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
