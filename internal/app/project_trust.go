package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// projectContext carries the effective system prompt and trust resolution for
// one run environment.
type projectContext struct {
	systemPrompt string
	trust        trust.Resolution
}

// resolveProjectTrust discovers protected project resources, resolves the
// trust decision, and persists any interactive choice. It must run before
// the interactive TUI starts so the trust prompt is its own terminal
// program rather than a main-UI slash command.
func (a *application) resolveProjectTrust(
	ctx context.Context,
	workspace *tool.Workspace,
	configuration config.Config,
	override *bool,
	askUI trust.AskFunc,
) (trust.Resolution, error) {
	if workspace == nil {
		return trust.Resolution{}, fmt.Errorf("app: workspace is required")
	}
	root := workspace.PhysicalPath()
	snapshot, err := trust.Discover(root)
	if err != nil {
		return trust.Resolution{}, fmt.Errorf("app: discover project resources: %w", err)
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
		return trust.Resolution{}, fmt.Errorf("app: resolve project trust: %w", err)
	}
	if resolution.Source == trust.SourceInteractive &&
		len(resolution.Choice.Updates) > 0 {
		if err := store.SetMany(resolution.Choice.Updates); err != nil {
			return trust.Resolution{}, fmt.Errorf(
				"app: persist project trust: %w",
				err,
			)
		}
	}
	return resolution, nil
}

// resolveProjectContext resolves trust and assembles the prompt-file view of
// the system prompt. Production startup uses resolveProjectTrust, then skill
// discovery, then assembleSystemPrompt so the catalog can feed tools and
// guard. Tests keep this helper for prompt-file behavior with an empty catalog.
func (a *application) resolveProjectContext(
	ctx context.Context,
	workspace *tool.Workspace,
	configuration config.Config,
	override *bool,
	askUI trust.AskFunc,
	tools []agent.Tool,
) (projectContext, error) {
	resolution, err := a.resolveProjectTrust(
		ctx,
		workspace,
		configuration,
		override,
		askUI,
	)
	if err != nil {
		return projectContext{}, err
	}
	systemPrompt, err := assembleSystemPrompt(
		workspace,
		configuration,
		resolution.Decision,
		tools,
		skill.Catalog{},
	)
	if err != nil {
		return projectContext{}, err
	}
	return projectContext{
		systemPrompt: systemPrompt,
		trust:        resolution,
	}, nil
}
