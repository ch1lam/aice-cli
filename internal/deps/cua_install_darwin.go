package deps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

func cuaVerifyCommand(ctx context.Context, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "CUA_DRIVER_RS_UPDATE_CHECK=false"}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	var output cuaVerifyOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", errors.Join(ctx.Err(), err)
	}
	return output.String(), nil
}

type cuaVerifyOutput struct{ buffer bytes.Buffer }

func (w *cuaVerifyOutput) String() string { return w.buffer.String() }

func (w *cuaVerifyOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 8192 {
		return 0, errors.New("Cua verification output exceeds limit")
	}
	return w.buffer.Write(p)
}
