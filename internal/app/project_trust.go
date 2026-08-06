package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// projectContext carries the effective system prompt and trust resolution for
// one run environment.
type projectContext struct {
	systemPrompt string
	trust        trust.Resolution
}

// resolveProjectContext discovers protected project resources, resolves the
// trust decision, persists any interactive choice, and assembles the effective
// system prompt. It must run before the interactive TUI starts so the trust
// prompt is its own terminal program rather than a main-UI slash command.
func (a *application) resolveProjectContext(
	ctx context.Context,
	workspace *tool.Workspace,
	configuration config.Config,
	override *bool,
	askUI trust.AskFunc,
) (projectContext, error) {
	if workspace == nil {
		return projectContext{}, fmt.Errorf("app: workspace is required")
	}
	root := workspace.PhysicalPath()
	snapshot, err := trust.Discover(root)
	if err != nil {
		return projectContext{}, fmt.Errorf("app: discover project resources: %w", err)
	}

	store := trust.NewStore(configuration.Paths.GlobalTrust)
	resolution, err := store.Resolve(trust.ResolveOptions{
		CWD:      root,
		Snapshot: snapshot,
		Override: override,
		Policy:   configuration.DefaultProjectTrust,
		AskUI:    askUI,
	})
	if err != nil {
		return projectContext{}, fmt.Errorf("app: resolve project trust: %w", err)
	}
	if resolution.Source == trust.SourceInteractive &&
		len(resolution.Choice.Updates) > 0 {
		if err := store.SetMany(resolution.Choice.Updates); err != nil {
			return projectContext{}, fmt.Errorf(
				"app: persist project trust: %w",
				err,
			)
		}
	}

	systemPrompt, err := assembleSystemPrompt(
		workspace,
		configuration,
		resolution.Decision,
	)
	if err != nil {
		return projectContext{}, err
	}
	return projectContext{
		systemPrompt: systemPrompt,
		trust:        resolution,
	}, nil
}
