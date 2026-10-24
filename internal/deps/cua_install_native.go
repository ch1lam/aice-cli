package deps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// These file digests come from the checksum-verified full release archives.
// Reuse checks bytes again; a version string or a mutable receipt is not proof
// that a user-writable installation still contains the pinned release.
func cuaNativeFiles(goos, goarch string) (map[string]string, error) {
	switch goos + "/" + goarch {
	case "linux/arm64":
		return map[string]string{
			"cua-driver":       "26e9eee8165e9ce7b614b5e3d68e5d963231385a82b65236406718b5ce1ff936",
			"cua-cursor-theme": "62677c2e25678a2a55278fe68179b92435c1d3b376871caa64912a02ebf70c97",
		}, nil
	case "linux/amd64":
		return map[string]string{
			"cua-driver":       "bf57b2f3778a0a7daa9388e241f20ce948b32a7a2f4dd102e497f34e54137934",
			"cua-cursor-theme": "5e203a2ce6ed0abe575884030f04973e96858ef586679297fe171a74a1c1c0e8",
		}, nil
	case "windows/arm64":
		return map[string]string{
			"cua-driver.exe":       "2d6a0822be887a8a51678f57682d7362d484d4049b769ef964f0c2dffda29de1",
			"cua-cursor-theme.exe": "7b2e64ccd1ee6198b5b76ea49f793e2f7dfc48823bf829cdab6562c41a2b4908",
			"cua-driver-uia.exe":   "51971aa82f70b4a0eebd55ed8e7e628c979399317ec07bd41107d7203d0630c7",
		}, nil
	case "windows/amd64":
		return map[string]string{
			"cua-driver.exe":       "ffa58d88779ee219304b0c307f4cfa970fe822f830c6ecf9254eda5c29e22bf2",
			"cua-cursor-theme.exe": "ffaf56a62b0f3bdaf65f2bc30612e5d6ed408362d4868d6f20e8db93be851f0c",
			"cua-driver-uia.exe":   "69aa6ea0049ce59dfd14593be77c0632f12f6eda3d4a2f4bab1a00e3a72a9754",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported Cua native installation: %s/%s", goos, goarch)
	}
}

type cuaNativeInstaller struct {
	options  Options
	artifact CuaArtifact
	files    map[string]string
	verify   func(context.Context, string) error
	publish  func(string, string) error
}

func (i cuaNativeInstaller) install(ctx context.Context) (result CuaInstallResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := filepath.Abs(filepath.Join(i.options.BinDir, "cua", CuaDriverVersion))
	if err != nil {
		return result, err
	}
	destination := filepath.Join(root, i.options.Goos+"-"+i.options.Goarch)
	name := "cua-driver"
	if i.options.Goos == "windows" {
		name += ".exe"
	}
	installation := CuaInstallation{Binary: filepath.Join(destination, name), Version: CuaDriverVersion}
	reuse := func() (bool, error) {
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if err := i.verifyInstallation(ctx, destination); err != nil {
			return false, fmt.Errorf("Cua installation already exists but is incompatible; existing files were preserved: %w", err)
		}
		result.Installation, result.Reused = installation, true
		return true, nil
	}
	if exists, err := reuse(); exists || err != nil {
		return result, err
	}
	if i.options.NoInstall {
		return result, fmt.Errorf("%w; Cua download disabled by current no_dep_install preference", ErrCuaNotInstalled)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return result, err
	}
	unlock, err := lockCuaInstall(ctx, root)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := unlock(); err != nil {
			result.Warnings = append(result.Warnings, "Installation lock cleanup failed: "+err.Error())
		}
	}()
	if exists, err := reuse(); exists || err != nil {
		return result, err
	}
	url := strings.TrimRight(i.options.BaseURL, "/") + "/trycua/cua/releases/download/cua-driver-rs-v" + CuaDriverVersion + "/" + i.artifact.Name
	archive, err := download(ctx, i.options, url, i.artifact.SHA256, archiveSuffix(i.artifact.Name))
	if err != nil {
		return result, err
	}
	defer func() {
		if err := os.Remove(archive); err != nil {
			result.Warnings = append(result.Warnings, "Downloaded archive cleanup failed: "+err.Error())
		}
	}()
	stage, err := os.MkdirTemp(root, ".aice-cua-")
	if err != nil {
		return result, err
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			result.Warnings = append(result.Warnings, "Cua staging cleanup failed: "+err.Error())
		}
	}()
	if err := extractCuaNative(ctx, archive, stage, i.artifact.Name, i.files); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(stage, "LICENSE"), cuaLicense, 0644); err != nil {
		return result, err
	}
	if err := i.verifyInstallation(ctx, stage); err != nil {
		return result, fmt.Errorf("Cua staged installation verification failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := i.publish(stage, destination); err != nil {
		return result, fmt.Errorf("publish Cua installation (existing files preserved): %w", err)
	}
	result.Installation, result.Installed = installation, true
	return result, nil
}

func (i cuaNativeInstaller) verifyInstallation(ctx context.Context, directory string) error {
	if err := verifyCuaNativeFiles(ctx, directory, i.files); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(directory, "LICENSE"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(cuaLicense)) {
		return errors.New("Cua installation license is missing or changed")
	}
	license, err := os.ReadFile(filepath.Join(directory, "LICENSE"))
	if err != nil {
		return err
	}
	if !bytes.Equal(license, cuaLicense) {
		return errors.New("Cua installation license is changed")
	}
	return i.verify(ctx, directory)
}

func verifyCuaNativeFiles(ctx context.Context, directory string, files map[string]string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Cua installation must be a real directory")
	}
	// Extra files could alter dynamic loading even when the executable is pinned.
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, ok := files[entry.Name()]; !ok && entry.Name() != "LICENSE" {
			return errors.New("unexpected file in Cua native installation")
		}
	}
	for name, want := range files {
		filename := filepath.Join(directory, name)
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return fmt.Errorf("Cua file is not a bounded regular file: %s", name)
		}
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: f}, (256<<20)+1))
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return err
		}
		if fmt.Sprintf("%x", hash.Sum(nil)) != want {
			return fmt.Errorf("Cua installed file digest mismatch: %s", name)
		}
	}
	return ctx.Err()
}
