package desktop

// These shapes describe synthetic fixture requests only. They are not a second
// production action API or authorization layer; every operation uses CallChecked.
type Point struct{ X, Y float64 }
type DragGesture struct {
	From, To   *Point
	DurationMS int
}

func fixtureActionArguments(binding observationBinding, request ActRequest) (string, map[string]any) {
	name := request.Kind
	args := map[string]any{"pid": binding.target.PID, "window_id": binding.target.WindowID}
	if request.ElementToken != "" {
		args["element_token"] = request.ElementToken
	}
	if request.DeliveryMode != "" {
		args["delivery_mode"] = request.DeliveryMode
	}
	if request.Point != nil {
		// Fixtures choose points on their prepared test image. Convert back to the
		// Driver source frame exactly as a model reading generic image metadata does.
		args["x"], args["y"] = fixtureSourcePoint(binding, request.Point)
		if name == "click" || name == "double_click" || name == "right_click" {
			args["capture_id"] = binding.capture
		}
	}
	switch name {
	case "double_click":
		name = "click"
		args["count"] = 2
	case "right_click":
		name = "click"
		args["button"] = "right"
	case "key":
		name = "press_key"
	}
	if request.Kind == "set_value" {
		args["value"] = request.Text
	} else if request.Text != "" {
		args["text"] = request.Text
	}
	if request.Key != "" {
		args["key"] = request.Key
	}
	if len(request.Keys) > 0 {
		args["keys"] = request.Keys
	}
	if request.Direction != "" {
		args["direction"] = request.Direction
	}
	if request.Amount != 0 {
		args["amount"] = request.Amount
	}
	if request.Drag != nil {
		args["from_x"], args["from_y"] = fixtureSourcePoint(binding, request.Drag.From)
		args["to_x"], args["to_y"] = fixtureSourcePoint(binding, request.Drag.To)
		if request.Drag.DurationMS != 0 {
			args["duration_ms"] = request.Drag.DurationMS
		}
	}
	return name, args
}

func fixtureSourcePoint(binding observationBinding, point *Point) (float64, float64) {
	if binding.width == 0 || binding.height == 0 {
		return point.X, point.Y
	}
	return point.X * float64(binding.sourceWidth) / float64(binding.width), point.Y * float64(binding.sourceHeight) / float64(binding.height)
}
