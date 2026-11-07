package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const autoModeKey = "auto_mode"

// AutoModeSettings extend the auto mode classifier, which runs on
// small_model. Lists append to the built-in template. Only user settings may
// add allow exceptions; trusted project settings may add environment facts and
// block rules, which can only make the classifier stricter.
type AutoModeSettings struct {
	Environment []string `json:"environment,omitempty"`
	Block       []string `json:"block,omitempty"`
	Allow       []string `json:"allow,omitempty"`
}

func (a *AutoModeSettings) extract(path string, values map[string]any, paths Paths, diagnostics *[]string) error {
	raw, ok := values[autoModeKey]
	if !ok {
		return nil
	}
	delete(values, autoModeKey)
	if path == paths.GlobalAuth {
		*diagnostics = append(*diagnostics, "Ignored auto_mode in the user auth file: it belongs in settings")
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var layer AutoModeSettings
	if err := jsonutil.DecodeStrict(data, &layer); err != nil {
		return fmt.Errorf("config: auto_mode must be an object with environment, block and allow: %w", err)
	}
	for _, list := range [][]string{layer.Environment, layer.Block, layer.Allow} {
		for _, entry := range list {
			if strings.TrimSpace(entry) == "" || len(entry) > 1000 {
				return fmt.Errorf("config: auto_mode entries must be non-empty and at most 1000 bytes")
			}
		}
	}
	if path == paths.ProjectSettings && len(layer.Allow) > 0 {
		*diagnostics = append(*diagnostics, "Ignored auto_mode.allow in project settings: only user settings may relax the classifier")
		layer.Allow = nil
	}
	a.Environment = append(a.Environment, layer.Environment...)
	a.Block = append(a.Block, layer.Block...)
	a.Allow = append(a.Allow, layer.Allow...)
	return nil
}
