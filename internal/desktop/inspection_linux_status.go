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
	before, err := s.service.inspect(ctx)
	if err != nil {
		return report, err
	}
	if err := s.peer(ctx, before.pid); err != nil {
		return report, err
	}
	c, err := s.connect(ctx)
	if err != nil {
		return report, err
	}
	defer func() { returnErr = errors.Join(returnErr, c.close(), ctx.Err()) }()
	configuration, err := c.call(ctx, "get_config", map[string]any{})
	if err != nil {
		return report, err
	}
	var version struct{ Version, Platform string }
	if configuration.IsError || json.Unmarshal(configuration.Structured, &version) != nil || version.Version != DriverVersion || version.Platform != "linux" {
		return report, serviceError("incompatible_service", "the connected Cua service does not match the pinned Linux runtime")
	}
	permissions, err := c.call(ctx, "check_permissions", map[string]any{})
	if err != nil {
		return report, err
	}
	facts, err := readLinuxPermissions(permissions)
	if err != nil {
		return report, err
	}
	after, err := s.service.inspect(ctx)
	if err != nil {
		return report, err
	}
	if before.pid != after.pid {
		return report, serviceError("service_changed", "Cua service changed during inspection")
	}
	if err := s.peer(ctx, after.pid); err != nil {
		return report, err
	}
	report.ConnectionVerified, report.Linux = true, &facts
	return report, nil
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
