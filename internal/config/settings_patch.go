package config

import "context"

// SaveSettingsPatch updates only explicitly named user fields. It preserves
// unknown keys and shadowed unrelated invalid values, and never reloads them
// into the running instance. Call WithPatch before preparing dependencies.
func SaveSettingsPatch(ctx context.Context, paths Paths, patch SettingsPatch) (CommitResult, error) {
	if err := paths.validate(); err != nil {
		return CommitResult{}, err
	}
	if err := patch.validate(); err != nil {
		return CommitResult{}, err
	}
	if len(patch.Changes) == 0 {
		return CommitResult{}, nil
	}
	// Validate only the changed values. Whole-candidate validation belongs to
	// WithPatch, which has access to the frozen layers and effective overrides.
	values := make(map[string]any)
	for _, change := range patch.Changes {
		if !change.Unset {
			values[string(change.ID)] = change.Value.raw()
		}
	}
	if _, err := decodeSettingMap(values); err != nil {
		return CommitResult{}, err
	}
	return editFile(ctx, paths.GlobalSettings, func(values map[string]any) error {
		for _, change := range patch.Changes {
			if change.Unset {
				delete(values, string(change.ID))
			} else {
				values[string(change.ID)] = change.Value.raw()
			}
		}
		return nil
	})
}
