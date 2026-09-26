package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// desktopState owns one instance's native manager and startup helper policy.
// Settings candidates and tools reuse this owner; each active run freezes its
// own mode, image capability, native session and cancellation in bindContext.
type desktopState struct {
	bind           func(context.Context, desktop.RunOptions) (tool.DesktopBackend, func() error, error)
	close          func() error
	status         func() desktop.Status
	installOptions deps.Options
}

func (a *application) newDesktopState(configuration config.Config) (*desktopState, error) {
	if a.dependencies.newDesktop != nil {
		return a.dependencies.newDesktop(configuration)
	}
	home, homeErr := a.userHome()
	options := deps.DefaultOptions().WithBinDir(filepath.Join(home, ".aice", "bin")).WithNoInstall(configuration.NoDepInstall)
	manager, err := desktop.NewManager(func(ctx context.Context) (string, string, error) {
		if homeErr != nil || home == "" {
			return "", "", &desktop.ServiceError{Code: "setup_required", Detail: "Computer Use needs an available user home directory"}
		}
		// This path only reuses a verified installed App. Installing, starting a
		// service and requesting authorization belong to explicit setup.
		result, err := deps.InstallCua(ctx, options.WithNoInstall(true))
		if err != nil {
			return "", "", &desktop.ServiceError{Code: "setup_required", Detail: "Computer Use needs a compatible installed Driver; open Settings setup. " + err.Error()}
		}
		return result.Installation.Binary, filepath.Join(home, "Library", "Caches", "cua-driver", "cua-driver.sock"), nil
	})
	if err != nil {
		return nil, err
	}
	return &desktopState{installOptions: options, close: manager.Close, status: manager.Status,
		bind: func(ctx context.Context, options desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
			run, err := manager.Bind(ctx, options)
			if err != nil {
				return nil, nil, err
			}
			return run, run.Close, nil
		},
	}, nil
}

type desktopContextKey struct{}
type desktopRunBinding struct {
	owner   *desktopState
	backend tool.DesktopBackend
}

func (d *desktopState) bindContext(ctx context.Context, configuration config.Config, model llm.Model) (context.Context, func() error, error) {
	if !configuration.DesktopEnabled {
		return ctx, func() error { return nil }, nil
	}
	if d == nil || d.bind == nil {
		return ctx, nil, errors.New("app: Computer Use runtime is unavailable")
	}
	mode := desktop.ControlMode(configuration.DesktopControlMode)
	if mode == "" {
		mode = desktop.BackgroundOnly
	}
	runCtx, cancel := context.WithCancel(ctx)
	backend, closeRun, err := d.bind(runCtx, desktop.RunOptions{Mode: mode, Images: slices.Contains(model.InputModalities, llm.InputModalityImage)})
	if err != nil {
		cancel()
		return ctx, nil, err
	}
	if backend == nil || closeRun == nil {
		cancel()
		if closeRun != nil {
			_ = closeRun()
		}
		return ctx, nil, errors.New("app: Computer Use binding is incomplete")
	}
	closeBinding := sync.OnceValue(func() error {
		cancel()
		return closeRun()
	})
	return context.WithValue(runCtx, desktopContextKey{}, desktopRunBinding{owner: d, backend: backend}), closeBinding, nil
}

func (d *desktopState) bound(ctx context.Context) (tool.DesktopBackend, error) {
	if ctx == nil {
		return nil, errors.New("app: Computer Use context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binding, ok := ctx.Value(desktopContextKey{}).(desktopRunBinding)
	if !ok || d == nil || binding.owner != d || binding.backend == nil {
		return nil, errors.New("app: Computer Use requires an enabled active run binding")
	}
	return binding.backend, nil
}

func (d *desktopState) Apps(ctx context.Context, query string, limit int) (desktop.Discovery, error) {
	run, err := d.bound(ctx)
	if err != nil {
		return desktop.Discovery{}, err
	}
	return run.Apps(ctx, query, limit)
}
func (d *desktopState) Observe(ctx context.Context, request desktop.ObserveRequest) (desktop.Observation, error) {
	run, err := d.bound(ctx)
	if err != nil {
		return desktop.Observation{}, err
	}
	return run.Observe(ctx, request)
}
func (d *desktopState) Act(ctx context.Context, request desktop.ActRequest) (desktop.ActResult, error) {
	run, err := d.bound(ctx)
	if err != nil {
		return desktop.ActResult{}, err
	}
	return run.Act(ctx, request)
}
func (d *desktopState) Close() error {
	if d == nil || d.close == nil {
		return nil
	}
	return d.close()
}

// composeTools is shared by startup, Web replacement and scalar Settings
// publication. Optional capabilities never become part of the host baseTools.
func composeTools(base []agent.Tool, web webState, desktopState *desktopState, configuration config.Config) ([]agent.Tool, error) {
	result := append(slices.Clone(base), web.tools()...)
	if configuration.DesktopEnabled {
		if desktopState == nil {
			return nil, errors.New("app: Computer Use owner is missing")
		}
		tools, err := tool.NewDesktopTools(desktopState)
		if err != nil {
			return nil, err
		}
		for _, t := range tools {
			result = append(result, t)
		}
	}
	return result, nil
}
