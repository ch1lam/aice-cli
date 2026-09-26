package desktop

import (
	"context"
	"errors"
	"runtime"
	"time"
)

type PermissionState string

const (
	PermissionUnknown PermissionState = "Unknown"
	PermissionGranted PermissionState = "Granted"
	PermissionMissing PermissionState = "Missing"
)

// Inspection is a content-free, point-in-time read of the existing service.
// OS grants do not establish whether a future capture will succeed.
type Inspection struct {
	ConnectionVerified             bool
	Accessibility, ScreenRecording PermissionState
	CheckedAt                      time.Time
	Linux                          *LinuxInspection
}

func permissionState(granted bool) PermissionState {
	if granted {
		return PermissionGranted
	}
	return PermissionMissing
}

// Inspect never installs, starts or repairs a service. Its separate proxy only
// reads configuration and non-prompting permission facts; it never creates a Cua
// session, observes a window, captures, or invalidates executable references.
// The caller supplies a verified installed binary and bounds installation checks.
func Inspect(ctx context.Context, binary, endpoint string) (Inspection, error) {
	if runtime.GOOS == "linux" {
		return inspectLinuxService(ctx, binary, endpoint)
	}
	if runtime.GOOS != "darwin" {
		return Inspection{}, serviceError("platform_unavailable", "native Computer Use status is not yet integrated on this platform")
	}
	connector, err := newMacServiceConnector(binary, endpoint)
	if err != nil {
		return Inspection{}, err
	}
	return connector.inspection(ctx)
}

func (s *serviceConnector) inspection(ctx context.Context) (report Inspection, returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	report.Accessibility, report.ScreenRecording = PermissionUnknown, PermissionUnknown
	defer func() { report.CheckedAt = time.Now() }()
	c, err := s.admitInspection(ctx, false, &report)
	if err != nil {
		return report, err
	}
	closeErr := c.close()
	return report, errors.Join(ctx.Err(), closeErr)
}
