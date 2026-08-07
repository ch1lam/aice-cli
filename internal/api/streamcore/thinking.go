package streamcore

import (
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ThinkingEffort maps a thinking level to its canonical reasoning-effort token.
// The unknown level yields the empty token so the provider keeps its default.
func ThinkingEffort(level llm.ThinkingLevel) (string, error) {
	switch level {
	case llm.ThinkingLevelUnknown:
		return "", nil
	case llm.ThinkingLevelOff:
		return "off", nil
	case llm.ThinkingLevelMinimal:
		return "minimal", nil
	case llm.ThinkingLevelLow:
		return "low", nil
	case llm.ThinkingLevelMedium:
		return "medium", nil
	case llm.ThinkingLevelHigh:
		return "high", nil
	case llm.ThinkingLevelXHigh:
		return "xhigh", nil
	case llm.ThinkingLevelMax:
		return "max", nil
	default:
		return "", fmt.Errorf("unsupported thinking level %q", level)
	}
}
