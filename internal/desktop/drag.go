package desktop

import "errors"

// DragGesture is a left-button gesture entirely inside the observed window.
// Both points refer to the actual image delivered in that same observation.
type DragGesture struct {
	From       *Point `json:"from"`
	To         *Point `json:"to"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

func (r *Run) dragArguments(binding observationBinding, request ActRequest, delivery string) (string, map[string]any, error) {
	gesture := request.Drag
	if gesture == nil || gesture.From == nil || gesture.To == nil || request.Point != nil || request.ElementToken != "" {
		return "", nil, errors.New("desktop: drag requires from and to screenshot points, without point or element_token")
	}
	if binding.snapshot == "" {
		return "", nil, errors.New("desktop: drag requires a current session-owned screenshot snapshot")
	}
	fromX, fromY, err := binding.pixel(gesture.From.X, gesture.From.Y)
	if err != nil {
		return "", nil, err
	}
	toX, toY, err := binding.pixel(gesture.To.X, gesture.To.Y)
	if err != nil {
		return "", nil, err
	}
	duration := gesture.DurationMS
	if duration == 0 {
		duration = 500
	}
	if duration < 1 || duration > 10000 {
		return "", nil, errors.New("desktop: drag duration_ms must be 1..10000")
	}
	// The pinned drag schema does not accept capture_id. Like scroll and pixel
	// type_text it uses Cua's authoritative session-owned screenshot snapshot;
	// same-window publication by another owner is refused by screenshot_scale.
	return "drag", map[string]any{
		"session": r.id, "pid": binding.target.PID, "window_id": binding.target.WindowID,
		"from_x": fromX, "from_y": fromY, "to_x": toX, "to_y": toY,
		"duration_ms": duration, "delivery_mode": delivery,
	}, nil
}
