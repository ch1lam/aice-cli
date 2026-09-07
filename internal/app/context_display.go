package app

import (
	"fmt"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
)

func applyContextWindow(model llm.Model, configuration config.Config) llm.Model {
	if window := configuration.ContextWindows[string(model.Provider)+"/"+model.ID]; window > 0 {
		model.ContextWindow = window
	}
	return model
}

func displayContextWindow(model llm.Model, configuration config.Config) int64 {
	if window := configuration.ContextWindows[string(model.Provider)+"/"+model.ID]; window > 0 {
		return window
	}
	return model.ContextWindow
}

func contextWindowInformation(model llm.Model, configuration config.Config) string {
	window := displayContextWindow(model, configuration)
	if window <= 0 {
		return fmt.Sprintf("Context window: unknown (local budget %d; configure context_windows)", model.ContextWindow)
	}
	source := "provider catalog default"
	if model.Provider == custom.ProviderID {
		source = "custom fallback default"
	}
	if configuration.ContextWindows[string(model.Provider)+"/"+model.ID] > 0 {
		source = "context_windows override"
	}
	return fmt.Sprintf("Context window: %d tokens (%s)", window, source)
}
