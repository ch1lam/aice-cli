package app

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

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
	bind           func(context.Context, desktop.RunOptions) (managedDesktopRun, func() error, error)
	close          func() error
	status         func() desktop.Status
	installOptions deps.Options
	install        func(context.Context, deps.Options) (deps.CuaInstallResult, error)
	setup          func(context.Context, string, desktop.SetupOptions) (desktop.SetupResult, error)
	inspect        func(context.Context) (desktop.Inspection, error)
	healthMu       sync.Mutex
	setupCaptureAt time.Time

	// managedMu serializes local catalog construction with idle Settings
	// publication. The identity lasts across Runs, but never across a policy
	// replacement or Manager lifetime. No native work runs under this lock.
	managedMu       sync.Mutex
	managedIdentity string
	managedClosed   bool
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
		// Resolution only reuses a verified installed helper. The native manager
		// owns lazy runtime startup; installation and grants require setup.
		result, err := deps.InstallCua(ctx, options.WithNoInstall(true))
		if err != nil {
			return "", "", &desktop.ServiceError{Code: "setup_required", Detail: "Computer Use needs a compatible installed Driver; open Settings setup. " + err.Error()}
		}
		return result.Installation.Binary, desktopServiceEndpoint(home), nil
	})
	if err != nil {
		return nil, err
	}
	return &desktopState{installOptions: options, close: manager.Close, status: manager.Status,
		inspect: func(ctx context.Context) (desktop.Inspection, error) {
			if homeErr != nil || home == "" {
				return desktop.Inspection{}, errors.New("Computer Use needs an available user home directory")
			}
			result, err := deps.InstallCua(ctx, options.WithNoInstall(true))
			if err != nil {
				return desktop.Inspection{}, err
			}
			return desktop.Inspect(ctx, result.Installation.Binary, desktopServiceEndpoint(home))
		},
		install: func(ctx context.Context, options deps.Options) (deps.CuaInstallResult, error) {
			if homeErr != nil || home == "" {
				return deps.CuaInstallResult{}, errors.New("app: Computer Use needs an available user home directory")
			}
			return deps.InstallCua(ctx, options)
		},
		setup: func(ctx context.Context, binary string, options desktop.SetupOptions) (desktop.SetupResult, error) {
			if err := manager.Disconnect(ctx); err != nil {
				return desktop.SetupResult{}, err
			}
			return desktop.Setup(ctx, binary, desktopServiceEndpoint(home), options)
		},
		bind: func(ctx context.Context, options desktop.RunOptions) (managedDesktopRun, func() error, error) {
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
	owner           *desktopState
	backend         managedDesktopRun
	managedIdentity string
}

func (d *desktopState) bindContext(ctx context.Context, configuration config.Config, model llm.Model) (context.Context, func() error, error) {
	if !configuration.DesktopEnabled {
		// A disabled child Run cannot inherit an earlier enabled capability.
		return context.WithValue(ctx, desktopContextKey{}, desktopRunBinding{}), func() error { return nil }, nil
	}
	if d == nil || d.bind == nil {
		return ctx, nil, errors.New("app: Computer Use runtime is unavailable")
	}
	d.managedMu.Lock()
	if d.managedClosed {
		d.managedMu.Unlock()
		return ctx, nil, errors.New("app: Computer Use runtime is closed")
	}
	if d.managedIdentity == "" {
		d.managedIdentity = rand.Text()
	}
	identity := d.managedIdentity
	d.managedMu.Unlock()
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
	return context.WithValue(runCtx, desktopContextKey{}, desktopRunBinding{owner: d, backend: backend, managedIdentity: identity}), closeBinding, nil
}

func (d *desktopState) bound(ctx context.Context) (managedDesktopRun, error) {
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

func (d *desktopState) Close() error {
	if d == nil {
		return nil
	}
	d.managedMu.Lock()
	d.managedClosed = true
	d.managedIdentity = ""
	d.managedMu.Unlock()
	if d.close == nil {
		return nil
	}
	return d.close()
}

// composeTools is shared by startup, Web replacement and scalar Settings
// publication. Optional capabilities never become part of the host baseTools.
func composeTools(base []agent.Tool, web webState, desktopState *desktopState, configuration config.Config) ([]agent.Tool, error) {
	result := append(slices.Clone(base), web.tools()...)
	readResult, err := tool.NewToolResultRead(runResultReader{})
	if err != nil {
		return nil, err
	}
	result = append(result, readResult)
	if len(configuration.MCP.Servers) > 0 || configuration.DesktopEnabled {
		search, err := tool.NewToolSearch(mcpRunRouter{})
		if err != nil {
			return nil, err
		}
		result = append(result, search)
		resources, err := tool.NewMCPResourceList(mcpRunRouter{})
		if err != nil {
			return nil, err
		}
		result = append(result, resources)
		info, err := tool.NewMCPInfo(mcpRunRouter{})
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	if configuration.DesktopEnabled {
		if desktopState == nil {
			return nil, errors.New("app: Computer Use owner is missing")
		}
	}
	return result, nil
}

// Match the pinned release namespace without consulting PATH or project input.
func desktopServiceEndpoint(home string) string {
	return desktopServiceEndpointFor(home, runtime.GOOS)
}

func desktopServiceEndpointFor(home, goos string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "cua-driver", "cua-driver.sock")
	case "linux":
		return filepath.Join(home, ".cache", "cua-driver", "cua-driver.sock")
	case "windows":
		return `\\.\pipe\cua-driver`
	default:
		return ""
	}
}
