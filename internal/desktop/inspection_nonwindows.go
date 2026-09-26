//go:build !windows

package desktop

import "context"

func inspectWindowsService(context.Context, string, string) (Inspection, error) {
	return Inspection{}, serviceError("platform_unavailable", "Windows service inspection requires Windows")
}
