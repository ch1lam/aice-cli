//go:build !darwin && !linux && !windows

package deps

import (
	"context"
	"errors"
)

// InstallCua has no implicit upstream installer or autostart fallback.
func InstallCua(context.Context, Options) (CuaInstallResult, error) {
	return CuaInstallResult{}, errors.New("Cua native installer integration is not yet available on this platform")
}
