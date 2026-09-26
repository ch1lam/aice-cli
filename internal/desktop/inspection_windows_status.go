package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// WindowsInspection describes the admitted service, not target input readiness.
// UIA/PostMessage are upstream claims; UIAccess and SessionID come from its
// process. A nonzero session alone does not prove an unlocked desktop.
type WindowsInspection struct {
	IntegrityLevel                             string
	IntegrityRID                               *uint32
	UIAReported, PostMessageReported, UIAccess PermissionState
	SessionID                                  *uint32
}

type windowsServicePeer struct {
	sessionID uint32
	created   uint64
	uiAccess  PermissionState
}

type windowsInspector struct {
	service *serviceConnector
	peer    func(context.Context, int) (windowsServicePeer, error)
	connect func(context.Context) (driverClient, error)
}

func (s windowsInspector) inspect(ctx context.Context) (report Inspection, returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	report.Accessibility, report.ScreenRecording = PermissionUnknown, PermissionUnknown
	report.Windows = &WindowsInspection{UIAReported: PermissionUnknown, PostMessageReported: PermissionUnknown, UIAccess: PermissionUnknown}
	defer func() { report.CheckedAt = time.Now() }()
	before, err := s.service.inspect(ctx)
	if err != nil {
		return report, err
	}
	identity, err := s.peer(ctx, before.pid)
	if err != nil {
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
	if configuration.IsError || json.Unmarshal(configuration.Structured, &version) != nil || version.Version != DriverVersion || version.Platform != "windows" {
		return report, serviceError("incompatible_service", "the connected Cua service does not match the pinned Windows runtime")
	}
	permissions, err := c.call(ctx, "check_permissions", map[string]any{})
	if err != nil {
		return report, err
	}
	facts, err := readWindowsPermissions(permissions)
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
	current, err := s.peer(ctx, after.pid)
	if err != nil {
		return report, err
	}
	if identity.created != current.created || identity.sessionID != current.sessionID {
		return report, serviceError("service_changed", "Cua process identity changed during inspection")
	}
	facts.SessionID, facts.UIAccess = &current.sessionID, current.uiAccess
	report.ConnectionVerified, report.Windows = true, &facts
	return report, nil
}

func readWindowsPermissions(reply Reply) (WindowsInspection, error) {
	var raw struct {
		Elevated    *bool   `json:"elevated"`
		Integrity   string  `json:"integrity_level"`
		RID         *uint32 `json:"integrity_level_rid"`
		UIA         *bool   `json:"uia"`
		PostMessage *bool   `json:"post_message"`
	}
	unknown := serviceError("permissions_unknown", "Cua Windows integrity and input prerequisites are unknown")
	if reply.IsError || json.Unmarshal(reply.Structured, &raw) != nil || raw.Elevated == nil || raw.UIA == nil || raw.PostMessage == nil {
		return WindowsInspection{}, unknown
	}
	if raw.RID == nil {
		// The pinned helper reports elevated=false when its lookup failed.
		// Preserve Unavailable rather than interpreting that as Medium/Low.
		if raw.Integrity != "Unavailable" || *raw.Elevated {
			return WindowsInspection{}, unknown
		}
	} else {
		name := map[uint32]string{0: "Untrusted", 0x1000: "Low", 0x2000: "Medium", 0x2100: "Medium+", 0x3000: "High", 0x4000: "System"}[*raw.RID]
		if name == "" {
			name = "Unknown"
		}
		if raw.Integrity != name || *raw.Elevated != (*raw.RID >= 0x3000) {
			return WindowsInspection{}, unknown
		}
	}
	return WindowsInspection{IntegrityLevel: raw.Integrity, IntegrityRID: raw.RID,
		UIAReported: permissionState(*raw.UIA), PostMessageReported: permissionState(*raw.PostMessage), UIAccess: PermissionUnknown}, nil
}
