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
			"cua-driver":       "15eaf22b6dcb1a33fedad0e9704889d5cef26723b49f9531c34a54050d1bf8c8",
			"cua-cursor-theme": "7d65078398f73c2cd0572132e6913123374126f9cb48db6d55d6feb4450d0130",
		}, nil
	case "linux/amd64":
		return map[string]string{
			"cua-driver":       "312e4398ca7686df0c7d2f78d511dfc7a95b3f3958657da2149421186ae238e5",
			"cua-cursor-theme": "6c5cdb7ac1bfc3a34bcfabebe037f1ec9213c0736061a67341bb8fa3d42911e0",
		}, nil
	case "windows/arm64":
		return map[string]string{
			"cua-driver.exe":       "84763819f00547de24ccf33cb78cf3ab204e5a29b503b676f368698992aa8568",
			"cua-cursor-theme.exe": "2e31a078481fa02a78b773e8d3dad234320a34471f778254e7f9eea30739c5a3",
			"cua-driver-uia.exe":   "f70c439fd1653b665f08a6c851aa9359a12f4290947ecc345dfd12a93689d792",
		}, nil
	case "windows/amd64":
		return map[string]string{
			"cua-driver.exe":       "94bb765aad94e2fdf715c6c152c4e2b2b1f93977779a7569e5abc6466beaeab2",
			"cua-cursor-theme.exe": "84af3e2ac7cc2f2cb901929a3bdb96b59a1620f691ca979cadd913884a57a6df",
			"cua-driver-uia.exe":   "11d0b8c3e74b7b77f219633de7abc5ae50d650082582578961e950fca2343c0c",
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
