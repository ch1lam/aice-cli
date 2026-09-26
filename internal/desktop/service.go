package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ServiceError distinguishes external policy and identity failures from missing
// OS grants. A caller must not offer authorization as a remedy for every error.
type ServiceError struct {
	Code   string
	Detail string
}

func (e *ServiceError) Error() string { return "desktop: " + e.Detail }

func serviceError(code, detail string) error { return &ServiceError{Code: code, Detail: detail} }

// RuntimeResolver verifies an installed native helper and returns its exact
// binary and service endpoint. Resolution runs only on a cold connection; it
// must not install software or request OS permissions.
type RuntimeResolver func(context.Context) (binary, endpoint string, err error)

// NewManager creates a lazy manager without inspecting or starting the desktop.
// The application owns installation verification and configuration publication.
func NewManager(resolve RuntimeResolver) (*Manager, error) {
	if resolve == nil {
		return nil, errors.New("desktop: verified runtime resolver required")
	}
	manager := newManager(func(ctx context.Context) (driverClient, error) {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return nil, serviceError("platform_unavailable", "native Computer Use connection setup is not yet integrated on this platform")
		}
		binary, endpoint, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "linux" {
			return dialLinuxRuntime(ctx, binary, endpoint)
		}
		connector, err := newMacServiceConnector(binary, endpoint)
		if err != nil {
			return nil, err
		}
		native, err := newMacNativeService(connector)
		if err != nil {
			return nil, err
		}
		if _, err := native.ensure(ctx); err != nil {
			return nil, err
		}
		return connector.dial(ctx)
	})
	manager.platform = runtime.GOOS
	manager.occupy = func(ctx context.Context) (func() error, error) {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return nil, serviceError("platform_unavailable", "native Computer Use connection setup is not yet integrated on this platform")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "linux" {
			return lockDesktop(ctx, filepath.Join(home, ".cache", "cua-driver"))
		}
		return lockDesktop(ctx, filepath.Join(home, "Library", "Caches", "cua-driver"))
	}
	return manager, nil
}

// serviceConnector performs content-free, read-only admission on each cold
// connection. The application supplies a verified installed binary. Neither
// this connector nor its proxy may install, start, reconfigure or stop a daemon.
// NewManager is its application-facing construction path.
type serviceConnector struct {
	binary, endpoint string
	status           func(context.Context) (string, error)
	connect          func(context.Context) (driverClient, error)
}

func newMacServiceConnector(binary, endpoint string) (*serviceConnector, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(endpoint) {
		return nil, errors.New("desktop: verified absolute binary and service endpoint required")
	}
	return &serviceConnector{
		binary: binary, endpoint: endpoint,
		status: func(ctx context.Context) (string, error) {
			return serviceCommand(ctx, binary, "status", "--socket", endpoint)
		},
		connect: func(ctx context.Context) (driverClient, error) {
			transport, err := newProcessTransport(binary, endpoint)
			if err != nil {
				return nil, err
			}
			c, err := connect(ctx, transport)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}, nil
}

func (s *serviceConnector) dial(ctx context.Context) (driverClient, error) {
	return s.admit(ctx, true)
}

func (s *serviceConnector) admit(ctx context.Context, requireGrants bool) (driverClient, error) {
	return s.admitInspection(ctx, requireGrants, nil)
}

func (s *serviceConnector) admitInspection(ctx context.Context, requireGrants bool, report *Inspection) (driverClient, error) {
	// One bounded deadline covers both management probes and MCP admission.
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	before, err := s.inspect(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = c.close()
		}
	}()
	configuration, err := c.call(ctx, "get_config", map[string]any{})
	if err != nil {
		return nil, err
	}
	var version struct {
		Version  string `json:"version"`
		Platform string `json:"platform"`
	}
	if configuration.IsError || json.Unmarshal(configuration.Structured, &version) != nil || version.Version != DriverVersion || version.Platform != "macos" {
		return nil, serviceError("incompatible_service", "the connected Cua service does not match the pinned macOS runtime")
	}
	permission, err := c.call(ctx, "check_permissions", map[string]any{"prompt": false})
	if err != nil {
		return nil, err
	}
	grants, err := readMacPermissionIdentity(permission, s.binary, before.pid)
	if err != nil {
		return nil, err
	}
	if requireGrants && (!*grants.Accessibility || !*grants.ScreenRecording) {
		return nil, serviceError("setup_required", "Cua needs Accessibility and Screen Recording grants; open Computer Use setup")
	}
	// Mode is immutable for a daemon lifetime. Verify that the status endpoint
	// still names the same PID after MCP admission, without claiming ownership.
	after, err := s.inspect(ctx)
	if err != nil {
		return nil, err
	}
	if before.pid != after.pid {
		return nil, serviceError("service_changed", "Cua service changed during connection; reconnect before observing or acting")
	}
	if report != nil {
		report.ConnectionVerified = true
		report.Accessibility = permissionState(*grants.Accessibility)
		report.ScreenRecording = permissionState(*grants.ScreenRecording)
	}
	accepted = true
	return c, nil
}

func (s *serviceConnector) inspect(ctx context.Context) (serviceStatus, error) {
	output, err := s.status(ctx)
	if err != nil {
		return serviceStatus{}, fmt.Errorf("desktop: read-only Cua service status unavailable; open Computer Use setup: %w", err)
	}
	return parseServiceStatus(output, s.endpoint)
}

type serviceStatus struct{ pid int }

// Only the pinned release's public management command lacks structured output
// for authorization state. Parse its exact, content-free fields here; desktop
// observations and action success never use human-readable CLI output.
func parseServiceStatus(output, endpoint string) (serviceStatus, error) {
	unknown := func() (serviceStatus, error) {
		return serviceStatus{}, serviceError("service_unknown", "Cua service identity or authorization state could not be established")
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] != "Cua Driver daemon is running" {
		return unknown()
	}
	fields := make(map[string]string)
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok || fields[key] != "" {
			return unknown()
		}
		fields[key] = value
	}
	pid, err := strconv.Atoi(fields["pid"])
	if err != nil || pid <= 0 || fields["socket"] != endpoint {
		return unknown()
	}
	mode, source, ok := strings.Cut(fields["permission mode"], " (")
	if !ok || !strings.HasSuffix(source, ")") || source == ")" {
		return unknown()
	}
	if mode != "standard" {
		return serviceStatus{}, serviceError("external_restriction", "the existing Cua service is not in standard mode; AICE will not reconfigure it")
	}
	for _, key := range []string{"user policy", "managed policy"} {
		if fields[key] == "" {
			return unknown()
		}
		if fields[key] != "configured=false, active=false, valid=true" {
			return serviceStatus{}, serviceError("external_restriction", "the existing Cua service has an external or invalid authorization policy; AICE will not override it")
		}
	}
	if fields["capability manifest"] == "" {
		return unknown()
	}
	if fields["capability manifest"] != "configured=false, approved_at_startup=false, valid=true" {
		return serviceStatus{}, serviceError("external_restriction", "the existing Cua service has an external or invalid capability manifest; AICE will not override it")
	}
	return serviceStatus{pid: pid}, nil
}

type macPermissions struct {
	Accessibility   *bool `json:"accessibility"`
	ScreenRecording *bool `json:"screen_recording"`
	Source          struct {
		Attribution string `json:"attribution"`
		PID         int    `json:"pid"`
		Executable  string `json:"executable"`
		BundleID    string `json:"bundle_id"`
	} `json:"source"`
}

func readMacPermissionIdentity(reply Reply, binary string, pid int) (macPermissions, error) {
	var permission macPermissions
	if reply.IsError || json.Unmarshal(reply.Structured, &permission) != nil || permission.Accessibility == nil || permission.ScreenRecording == nil {
		return macPermissions{}, serviceError("permissions_unknown", "Cua OS permission status is unknown")
	}
	if permission.Source.Attribution != "driver-daemon" || permission.Source.PID != pid || permission.Source.Executable != binary || permission.Source.BundleID != "com.trycua.driver" {
		return macPermissions{}, serviceError("identity_mismatch", "Cua permission status does not belong to the verified signed App service")
	}
	// Grants are not capture evidence. Ignore historical direct_capture_* fields;
	// a real observation remains responsible for reporting capture availability.
	return permission, nil
}

func serviceCommand(ctx context.Context, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = driverEnvironment(os.Environ())
	var output, diagnostic serviceOutput
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 && len(args) == 3 && args[0] == "status" && args[1] == "--socket" && output.String() == "" && strings.TrimSpace(diagnostic.String()) == "Cua Driver daemon is not running" {
			return "", serviceError("not_running", "Cua service is not running")
		}
		return "", errors.Join(ctx.Err(), err)
	}
	return output.String(), nil
}

type serviceOutput struct{ buffer bytes.Buffer }

func (w *serviceOutput) String() string { return w.buffer.String() }

func (w *serviceOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 32*1024 {
		return 0, errors.New("desktop: service status exceeds output limit")
	}
	return w.buffer.Write(p)
}
