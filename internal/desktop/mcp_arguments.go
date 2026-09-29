package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Keep native options and their constraints intact. These exclusions are host
// authority boundaries, not a second implementation of Driver input semantics.
type managedTool struct {
	properties map[string]json.RawMessage
	session    bool
}

func managedSchema(raw json.RawMessage) (json.RawMessage, managedTool, error) {
	var schema map[string]json.RawMessage
	var tool managedTool
	if json.Unmarshal(raw, &schema) != nil || json.Unmarshal(schema["properties"], &tool.properties) != nil || tool.properties == nil {
		return nil, tool, errors.New("desktop: invalid admitted schema")
	}
	_, tool.session = tool.properties["session"]
	delete(tool.properties, "session")
	var required []string
	if len(schema["required"]) > 0 {
		if json.Unmarshal(schema["required"], &required) != nil {
			return nil, tool, errors.New("desktop: invalid admitted schema requirements")
		}
		for _, field := range required {
			if _, ok := tool.properties[field]; !ok {
				return nil, tool, errors.New("desktop: host policy removed a required Driver field")
			}
		}
	}
	schema["properties"], _ = json.Marshal(tool.properties)
	projected, err := json.Marshal(schema)
	return projected, tool, err
}

// Decode raw values without float64 conversion, preserving exact native IDs.
// Duplicate keys are rejected so policy and Driver cannot read different values.
func managedObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	fail := errors.New("desktop: arguments must be one JSON object with unique keys")
	if len(raw) > 1<<20 || !utf8.Valid(raw) {
		return nil, fail
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fail
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, fail
		}
		if _, exists := fields[key]; exists {
			return nil, fail
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, fail
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fail
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fail
	}
	return fields, nil
}

func (r *Run) managedArguments(tool managedTool, name string, raw json.RawMessage) (map[string]json.RawMessage, error) {
	args, err := managedObject(raw)
	if err != nil {
		return nil, err
	}
	for key := range args {
		if _, allowed := tool.properties[key]; !allowed {
			return nil, errors.New("desktop: argument is outside the published tool schema")
		}
	}
	if value, exists := args["delivery_mode"]; exists {
		if !jsonString(value, "background") && !jsonString(value, "foreground") {
			return nil, errors.New("desktop: delivery_mode must be background or foreground")
		}
		if jsonString(value, "foreground") && r.options.Mode != ForegroundAllowed {
			return nil, errors.New("desktop: this run permits background input only")
		}
	}
	if r.options.Mode == BackgroundOnly {
		for _, key := range []string{"scope", "coordinate_frame"} {
			if value, exists := args[key]; exists && !jsonString(value, "window") {
				return nil, errors.New("desktop: background-only mode does not permit desktop input")
			}
		}
		if value, exists := args["target"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			target, err := managedObject(value)
			if err != nil || !jsonString(target["kind"], "window") {
				return nil, errors.New("desktop: background-only mode requires a window target")
			}
		}
	}
	if !r.options.Images && ((name == "get_window_state" && !bytes.Equal(bytes.TrimSpace(args["include_screenshot"]), []byte("false"))) || nonemptyJSONText(args["screenshot_out_file"]) || nonemptyJSONText(args["debug_image_out"])) {
		return nil, errors.New("desktop: this model requires include_screenshot=false")
	}
	if tool.session {
		args["session"], _ = json.Marshal(r.id)
	}
	return args, nil
}

func jsonString(raw json.RawMessage, want string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == want
}

func nonemptyJSONText(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) != nil || value != ""
}
