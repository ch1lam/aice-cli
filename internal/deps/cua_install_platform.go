//go:build linux || windows

package deps

import (
	"context"
	"errors"
	"runtime"
)

// InstallCua provisions a private pinned native helper only on explicit setup.
// Startup resolution passes NoInstall; this does not authorize a desktop run.
func InstallCua(ctx context.Context, options Options) (CuaInstallResult, error) {
	options = normalize(options)
	if options.Goos != runtime.GOOS || options.Goarch != runtime.GOARCH {
		return CuaInstallResult{}, errors.New("Cua installation must run on its native platform and architecture")
	}
	artifact, err := CuaDriverArtifact(options.Goos, options.Goarch)
	if err != nil {
		return CuaInstallResult{}, err
	}
	files, err := cuaNativeFiles(options.Goos, options.Goarch)
	if err != nil {
		return CuaInstallResult{}, err
	}
	return (cuaNativeInstaller{options: options, artifact: artifact, files: files, verify: verifyCuaNative, publish: publishCuaNative}).install(ctx)
}
