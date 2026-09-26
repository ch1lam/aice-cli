package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// LinuxInspection distinguishes actual X11 connectivity and AT-SPI bus ownership
// from Wayland environment hints. None of these are capture or input evidence.
type LinuxInspection struct {
	X11, ATSPI, WaylandEnvironment, WaylandBackend, XSendEvent PermissionState
}

type linuxInspector struct {
	service *serviceConnector
	peer    func(context.Context, int) error
	connect func(context.Context) (driverClient, error)
}

func (s linuxInspector) inspect(ctx context.Context) (report Inspection, returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	report.Accessibility, report.ScreenRecording = PermissionUnknown, PermissionUnknown
	report.Linux = &LinuxInspection{PermissionUnknown, PermissionUnknown, PermissionUnknown, PermissionUnknown, PermissionUnknown}
	defer func() { report.CheckedAt = time.Now() }()
	c, facts, err := s.admit(ctx)
	if err != nil {
		return report, err
	}
	report.ConnectionVerified, report.Linux = true, &facts
	return report, errors.Join(c.close(), ctx.Err())
}

// admit retains the verified connection for a task or gives inspection its own
// disposable proxy. Neither path starts or reconfigures an existing service.
func (s linuxInspector) admit(ctx context.Context) (driverClient, LinuxInspection, error) {
	before, err := s.service.inspect(ctx)
	if err != nil {
		return nil, LinuxInspection{}, err
	}
	if err := s.peer(ctx, before.pid); err != nil {
		return nil, LinuxInspection{}, err
	}
	c, err := s.connect(ctx)
	if err != nil {
		return nil, LinuxInspection{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = c.close()
		}
	}()
	facts, err := readLinuxRuntime(ctx, c)
	if err != nil {
		return nil, LinuxInspection{}, err
	}
	after, err := s.service.inspect(ctx)
	if err != nil {
		return nil, LinuxInspection{}, err
	}
	if before.pid != after.pid {
		return nil, LinuxInspection{}, serviceError("service_changed", "Cua service changed during inspection")
	}
	if err := s.peer(ctx, after.pid); err != nil {
		return nil, LinuxInspection{}, err
	}
	accepted = true
	return c, facts, nil
}

func readLinuxRuntime(ctx context.Context, c driverClient) (LinuxInspection, error) {
	configuration, err := c.call(ctx, "get_config", map[string]any{})
	if err != nil {
		return LinuxInspection{}, err
	}
	var version struct{ Version, Platform string }
	if configuration.IsError || json.Unmarshal(configuration.Structured, &version) != nil || version.Version != DriverVersion || version.Platform != "linux" {
		return LinuxInspection{}, serviceError("incompatible_service", "the connected Cua service does not match the pinned Linux runtime")
	}
	permissions, err := c.call(ctx, "check_permissions", map[string]any{})
	if err != nil {
		return LinuxInspection{}, err
	}
	return readLinuxPermissions(permissions)
}

func requireLinuxX11(facts LinuxInspection) error {
	if facts.WaylandEnvironment != PermissionMissing || facts.WaylandBackend != PermissionMissing {
		return serviceError("display_unsupported", "native Wayland and XWayland action adapters are not verified; AICE will not send background input to compositor focus")
	}
	if facts.X11 != PermissionGranted {
		return serviceError("display_unavailable", "no usable X11 display; run AICE in the intended graphical session")
	}
	// AT-SPI may be absent for a pixel-only target. Individual observations
	// must report the unavailable semantic route, never manufacture elements.
	return nil
}

func readLinuxPermissions(reply Reply) (LinuxInspection, error) {
	var raw struct {
		X11            *bool `json:"x11"`
		ATSPI          *bool `json:"atspi"`
		Wayland        *bool `json:"wayland"`
		WaylandEnabled *bool `json:"wayland_enabled"`
		XSendEvent     *bool `json:"xsend_event"`
	}
	if reply.IsError || json.Unmarshal(reply.Structured, &raw) != nil || raw.X11 == nil || raw.ATSPI == nil || raw.Wayland == nil || raw.WaylandEnabled == nil || raw.XSendEvent == nil {
		return LinuxInspection{}, serviceError("permissions_unknown", "Cua Linux display and accessibility status is unknown")
	}
	return LinuxInspection{permissionState(*raw.X11), permissionState(*raw.ATSPI), permissionState(*raw.Wayland), permissionState(*raw.WaylandEnabled), permissionState(*raw.XSendEvent)}, nil
}
