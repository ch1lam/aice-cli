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
	resolution, err := a.chooseProjectTrust(workspace, configuration, override, askUI)
	if err != nil {
		return trust.Resolution{}, err
	}
	if err := persistProjectTrust(configuration.Paths, resolution); err != nil {
		return trust.Resolution{}, err
	}
	return resolution, nil
}

// chooseProjectTrust does not persist the choice until effective configuration
// has been validated. Project settings cannot authorize their own loading.
func (a *application) chooseProjectTrust(
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
	return resolution, nil
}

func persistProjectTrust(paths config.Paths, resolution trust.Resolution) error {
	if resolution.Source == trust.SourceInteractive &&
		len(resolution.Choice.Updates) > 0 {
		if err := trust.NewStore(paths.GlobalTrust).SetMany(resolution.Choice.Updates); err != nil {
			return fmt.Errorf(
				"app: persist project trust: %w",
				err,
			)
		}
	}
	return nil
}

func (a *application) loadRunModel(ctx context.Context, directory string, override *bool, askUI trust.AskFunc) (configuredModel, error) {
	if err := ctx.Err(); err != nil {
		return configuredModel{}, err
	}
	workspace, err := tool.NewWorkspace(directory)
	if err != nil {
		return configuredModel{}, fmt.Errorf("app: create workspace: %w", err)
	}
	var resolution *trust.Resolution
	configured, err := a.loadConfiguredModel(config.LoadOptions{
		Workspace: workspace.PhysicalPath(),
		TrustProject: func(paths config.Paths, policy trust.Default) (bool, error) {
			choice, err := a.chooseProjectTrust(workspace, config.Config{Paths: paths, DefaultProjectTrust: policy}, override, askUI)
			if err != nil {
				return false, err
			}
			resolution = &choice
			return choice.Decision == trust.DecisionTrusted, nil
		},
	})
	configured.projectTrust = resolution
	return configured, err
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
