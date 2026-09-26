//go:build !linux

package desktop

import "context"

func inspectLinuxService(context.Context, string, string) (Inspection, error) {
	return Inspection{}, serviceError("platform_unavailable", "Linux service inspection requires Linux")
}

func dialLinuxRuntime(context.Context, string, string) (driverClient, error) {
	return nil, serviceError("platform_unavailable", "Linux runtime requires Linux")
}
