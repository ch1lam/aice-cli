package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// InstallCua runs only for an explicit Settings setup action; Ensure never
// calls it. A compatible existing App can be reused without enabling downloads.
func InstallCua(ctx context.Context, options Options) (CuaInstallResult, error) {
	options = normalize(options)
	if options.Goos != runtime.GOOS {
		return CuaInstallResult{}, errors.New("Cua installation must run on its native platform")
	}
	artifact, err := CuaDriverArtifact(options.Goos, options.Goarch)
	if err != nil {
		return CuaInstallResult{}, err
	}
	return (cuaBundleInstaller{options: options, applications: "/Applications", artifact: artifact, verify: verifyCuaApp, publish: publishCuaApp}).install(ctx)
}

func publishCuaApp(from, to string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
}

func verifyCuaApp(ctx context.Context, bundle string) error {
	info, err := os.Lstat(bundle)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Cua App must be a real application directory")
	}
	requirement := `identifier "com.trycua.driver" and anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"`
	if _, err := cuaVerifyCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R="+requirement, bundle); err != nil {
		return fmt.Errorf("Cua signature or signing identity rejected: %w", err)
	}
	if _, err := cuaVerifyCommand(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", bundle); err != nil {
		return fmt.Errorf("Gatekeeper rejected Cua App: %w", err)
	}
	version, err := cuaVerifyCommand(ctx, filepath.Join(bundle, "Contents", "MacOS", "cua-driver"), "--version")
	if err != nil {
		return fmt.Errorf("Cua version check failed: %w", err)
	}
	if strings.TrimSpace(version) != "cua-driver "+CuaDriverVersion && strings.TrimSpace(version) != "cua-driver-rs "+CuaDriverVersion {
		return errors.New("Cua App version does not match the pinned release")
	}
	return nil
}
