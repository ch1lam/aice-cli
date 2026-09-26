package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// SetupResult retains external effects if authorization, admission or a later
// application preference save fails. Requested does not imply completion.
type SetupResult struct {
	LaunchRequested        bool
	AuthorizationRequested bool
	AuthorizationCompleted bool
	Ready                  bool
}

// Setup explicitly requests Cua's public OS grant and live capture verification
// flow. Only the user-facing setup action may call it, after disclosure. The
// supplied installed App must already have passed dependency verification.
// Cancellation stops our command, but cannot undo grants or close system UI.
func Setup(ctx context.Context, binary, endpoint string) (SetupResult, error) {
	if runtime.GOOS != "darwin" {
		return SetupResult{}, serviceError("platform_unavailable", "native Computer Use setup is not yet integrated on this platform")
	}
	connector, err := newMacServiceConnector(binary, endpoint)
	if err != nil {
		return SetupResult{}, err
	}
	native, err := newMacNativeService(connector)
	if err != nil {
		return SetupResult{}, err
	}
	return native.setup(ctx)
}

type nativeService struct {
	connector *serviceConnector
	lock      func(context.Context) (func() error, error)
	launch    func(context.Context) error
	grant     func(context.Context) error
}

func newMacNativeService(connector *serviceConnector) (*nativeService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	// The reviewed public grant command addresses only this default endpoint.
	// Never start a second endpoint and accidentally grant to a different host.
	want := filepath.Join(home, "Library", "Caches", "cua-driver", "cua-driver.sock")
	if connector.endpoint != want || connector.binary != "/Applications/CuaDriver.app/Contents/MacOS/cua-driver" {
		return nil, serviceError("identity_mismatch", "native setup requires the verified /Applications/CuaDriver.app and its user service endpoint")
	}
	return &nativeService{
		connector: connector,
		lock: func(ctx context.Context) (func() error, error) {
			return lockNativeSetup(ctx, filepath.Dir(connector.endpoint))
		},
		launch: func(ctx context.Context) error {
			return runNativeManagement(ctx, 10*time.Second, "/usr/bin/open", macLaunchArguments(connector.endpoint)...)
		},
		grant: func(ctx context.Context) error {
			return runNativeManagement(ctx, 4*time.Minute, connector.binary, "permissions", "grant")
		},
	}, nil
}

// LaunchServices owns the resulting daemon, not AICE. The startup gate switch
// suppresses the unsolicited permission UI only; OS checks and standard Cua
// authorization remain active. Admission still verifies grants before tools.
func macLaunchArguments(endpoint string) []string {
	return []string{"-n", "-g", "-a", "/Applications/CuaDriver.app",
		"--env", "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "--env", "CUA_DRIVER_RS_UPDATE_CHECK=false",
		"--env", "CUA_DRIVER_PERMISSION_MODE=standard", "--env", "CUA_DRIVER_EMBEDDED=0",
		"--args", "serve", "--permission-mode", "standard", "--no-permissions-gate", "--socket", endpoint}
}

// ensure is lazy startup for an already enabled run. It never requests grants,
// captures, restarts a daemon, or treats an arbitrary status error as absence.
func (n *nativeService) ensure(ctx context.Context) (requested bool, returnErr error) {
	if _, err := n.connector.inspect(ctx); err == nil {
		return false, nil
	} else if !serviceHasCode(err, "not_running") {
		return false, err
	}
	unlock, err := n.lock(ctx)
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	return n.startLocked(ctx)
}

func (n *nativeService) startLocked(ctx context.Context) (bool, error) {
	if _, err := n.connector.inspect(ctx); err == nil {
		return false, nil
	} else if !serviceHasCode(err, "not_running") {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := n.launch(ctx); err != nil {
		return true, fmt.Errorf("desktop: service launch requested but readiness unknown: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if _, err := n.connector.inspect(ctx); err == nil {
			return true, nil
		} else if !serviceHasCode(err, "not_running") {
			return true, err
		}
		if err := waitNativeProbe(ctx); err != nil {
			return true, err
		}
	}
}

func (n *nativeService) setup(ctx context.Context) (result SetupResult, returnErr error) {
	unlock, err := n.lock(ctx)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	result.LaunchRequested, err = n.startLocked(ctx)
	if err != nil {
		return result, err
	}
	// Validate version, standard mode and exact signed daemon identity before
	// requesting any grant. Missing grants alone are expected at this point.
	c, err := n.connector.admit(ctx, false)
	if err != nil {
		return result, err
	}
	if err := c.close(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.AuthorizationRequested = true
	if err := n.grant(ctx); err != nil {
		return result, fmt.Errorf("desktop: authorization was requested; grants may have changed, verify before retrying: %w", err)
	}
	// Pinned `permissions grant` exits successfully only after its explicit live
	// direct-capture probe. This is a point-in-time fact, not permanent readiness.
	result.AuthorizationCompleted = true
	c, err = n.connector.dial(ctx)
	if err != nil {
		return result, err
	}
	if err := c.close(); err != nil {
		return result, err
	}
	result.Ready = true
	return result, nil
}

func serviceHasCode(err error, code string) bool {
	var failure *ServiceError
	return errors.As(err, &failure) && failure.Code == code
}

func lockNativeSetup(ctx context.Context, directory string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	path := filepath.Join(directory, ".aice-setup.lock")
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("desktop: setup lock unavailable: %w", err)
		}
		if err := os.Mkdir(path, 0700); err == nil {
			return func() error { return os.Remove(path) }, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// Never steal a foreign/crashed holder's lock or stop its service.
		if err := waitNativeProbe(ctx); err != nil {
			return nil, err
		}
	}
}

func waitNativeProbe(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runNativeManagement(ctx context.Context, timeout time.Duration, binary string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = driverEnvironment(os.Environ())
	cmd.WaitDelay = time.Second
	// Operator prompts are described by Settings, never copied to transcript or
	// logs. Native permission UI belongs to the signed App and the operating OS.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return errors.Join(cmd.Run(), ctx.Err())
}
