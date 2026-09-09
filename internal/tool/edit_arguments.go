package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type editArguments struct {
	Path  string
	Edits []replacement
}

// Decode each entry separately so invalid fields retain their array index.
// Only fully validated replacements reach file access and matching.
func decodeEditArguments(ctx context.Context, call llm.ToolCall) (editArguments, error) {
	type arguments struct {
		Path  json.RawMessage   `json:"path"`
		Edits []json.RawMessage `json:"edits"`
	}
	raw, err := decodeArguments[arguments](ctx, call, "edit")
	if err != nil {
		return editArguments{}, err
	}
	path, err := editArgumentString(raw.Path, "path")
	if err != nil {
		return editArguments{}, err
	}
	if len(raw.Edits) == 0 {
		return editArguments{}, fmt.Errorf("tool \"edit\": edits must contain at least one replacement")
	}
	args := editArguments{Path: path, Edits: make([]replacement, 0, len(raw.Edits))}
	for index, data := range raw.Edits {
		var entry *struct {
			OldText json.RawMessage `json:"oldText"`
			NewText json.RawMessage `json:"newText"`
		}
		if err := jsonutil.DecodeStrict(data, &entry); err != nil {
			return editArguments{}, fmt.Errorf("tool \"edit\": edits[%d]: %w", index, err)
		}
		if entry == nil {
			return editArguments{}, fmt.Errorf("tool \"edit\": edits[%d] must be an object", index)
		}
		oldText, err := editArgumentString(entry.OldText, fmt.Sprintf("edits[%d].oldText", index))
		if err != nil {
			return editArguments{}, err
		}
		if oldText == "" {
			return editArguments{}, fmt.Errorf("tool \"edit\": edits[%d].oldText must not be empty", index)
		}
		newText, err := editArgumentString(entry.NewText, fmt.Sprintf("edits[%d].newText", index))
		if err != nil {
			return editArguments{}, err
		}
		args.Edits = append(args.Edits, replacement{OldText: oldText, NewText: newText})
	}
	return args, nil
}

func editArgumentString(data json.RawMessage, field string) (string, error) {
	var value *string
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return "", fmt.Errorf("tool \"edit\": %s is required and must be a string", field)
	}
	return *value, nil
}
