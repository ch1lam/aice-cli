package app

import (
	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
)

func runLimits(c config.Config) agent.RunLimits {
	return agent.RunLimits{MaxTurns: c.MaxTurns, Tokens: c.RunTokenBudget, Timeout: c.RunTimeout, NoProgress: c.RunNoProgressLimit}
}
