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
			"cua-driver":       "85f6841061f0c2347c84c6a308a09a3fb7e41481de71ba479777e3d808ec50cd",
			"cua-cursor-theme": "01bab17abde1c6a6d9d7eb1fea35e7062c22904c955bd9267b707adb61c60b0b",
		}, nil
	case "linux/amd64":
		return map[string]string{
			"cua-driver":       "a9c3262817103cdff6c09e351f6a3410206624a6f40eea5bd14b4abb3ddf9362",
			"cua-cursor-theme": "49cd40354577a6c6a6ff7e1954ed09787d4b2c7b6a445bb835dd0481f4e42181",
		}, nil
	case "windows/arm64":
		return map[string]string{
			"cua-driver.exe":       "d3637107871cca8fa7109a586a1e2f8412cae30deefc55c266b90976acefd06d",
			"cua-cursor-theme.exe": "701c540406a89f1c65495ccba0fc0f93a2e4b945ca30616953db6e1ac8788f38",
			"cua-driver-uia.exe":   "e4210a12f778b9ff503f3119845fe3ffb81d2e9a4ced0ba4791ddbb8b7c2b2f0",
		}, nil
	case "windows/amd64":
		return map[string]string{
			"cua-driver.exe":       "c0dc4bdf8d2b24e785769c82fb77fc618b9f3b2263e113d565167e4e38acda57",
			"cua-cursor-theme.exe": "495cd396fd5dd4d0b744f941b737cb59eb69673ba2a812b011dfd94e6e1b82a8",
			"cua-driver-uia.exe":   "0e170fd6f66190d15afeb26f90a051d9088b794252a5d26f5099c83227662d23",
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
		_, copyErr := io.Copy(hash, io.LimitReader(&cuaContextReader{ctx: ctx, reader: f}, (256<<20)+1))
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return err
		}
		if fmt.Sprintf("%x", hash.Sum(nil)) != want {
			return fmt.Errorf("Cua installed file digest mismatch: %s", name)
		}
	}
	return ctx.Err()
}
