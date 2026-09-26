//go:build !linux

package desktop

import "context"

func inspectLinuxService(context.Context, string, string) (Inspection, error) {
	return Inspection{}, serviceError("platform_unavailable", "Linux service inspection requires Linux")
}
