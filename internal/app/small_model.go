package app

import "fmt"

// smallModel resolves small_model on the same provider as configured, or
// returns configured unchanged when it is unset or names the same model. Side
// calls (the auto mode classifier, permission summaries, Session titles) run
// on it so they stay fast and cheap.
func (a *application) smallModel(configured configuredModel) (configuredModel, error) {
	if name := configured.configuration.SmallModel; name != "" && name != configured.model.ID {
		cfg := configured.configuration
		cfg.Model = name
		model, options, err := resolveModelSettings(a.dependencies.providers, cfg)
		if err != nil {
			return configuredModel{}, fmt.Errorf("small_model: %w", err)
		}
		configured = configuredModel{configuration: cfg, model: model, options: options}
	}
	if err := a.initializeConfiguredModel(&configured); err != nil {
		return configuredModel{}, err
	}
	return configured, nil
}
