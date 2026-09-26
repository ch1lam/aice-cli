package desktop

import (
	_ "embed"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
)

// Pin the complete schemas, including defaults and descriptions, to the same
// reviewed release as the executable. See schema/README.md for provenance.
// This is contract comparison, not another JSON Schema implementation.
//
//go:embed schema/macos-0.29.1.json
var macSchemaInventory []byte

//go:embed schema/linux-status-0.29.1.json
var linuxStatusSchemaInventory []byte

type schemaInventory struct {
	Version  string                     `json:"version"`
	Platform string                     `json:"platform"`
	Tools    map[string]json.RawMessage `json:"tools"`
}

func reviewedMacTools(actual map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	return reviewedTools(actual, macSchemaInventory, "macos")
}

func reviewedLinuxStatusTools(actual map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	return reviewedTools(actual, linuxStatusSchemaInventory, "linux")
}

func reviewedTools(actual map[string]json.RawMessage, data []byte, platform string) (map[string]json.RawMessage, error) {
	var inventory schemaInventory
	if json.Unmarshal(data, &inventory) != nil || inventory.Version != DriverVersion || inventory.Platform != platform || len(inventory.Tools) == 0 {
		return nil, errors.New("desktop: invalid built-in Cua schema inventory")
	}
	reviewed := make(map[string]json.RawMessage, len(inventory.Tools))
	for _, name := range slices.Sorted(maps.Keys(inventory.Tools)) {
		var expected, received any
		if json.Unmarshal(inventory.Tools[name], &expected) != nil {
			return nil, errors.New("desktop: invalid built-in Cua tool schema")
		}
		if json.Unmarshal(actual[name], &received) != nil || !reflect.DeepEqual(received, expected) {
			return nil, serviceError("incompatible_service", "Cua tool "+name+" does not match the reviewed "+platform+" schema; no desktop action was dispatched")
		}
		reviewed[name] = actual[name]
	}
	// Unknown upstream tools are deliberately not admitted, even when harmless.
	// Adding a tool requires a typed adapter and a reviewed schema pin.
	return reviewed, nil
}
