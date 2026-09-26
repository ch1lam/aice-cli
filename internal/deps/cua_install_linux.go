package deps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func publishCuaNative(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}

func verifyCuaNative(ctx context.Context, directory string) error {
	version, err := cuaVerifyCommand(ctx, filepath.Join(directory, "cua-driver"), "--version")
	if err != nil {
		return fmt.Errorf("Cua native version probe failed; check architecture and system libraries (libX11, libXi, libxkbcommon, glibc): %w", err)
	}
	if strings.TrimSpace(version) != "cua-driver "+CuaDriverVersion {
		return errors.New("Cua native version does not match the pinned release")
	}
	return nil
}
