package deps

import (
	"archive/tar"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed cua/LICENSE
var cuaLicense []byte

// CuaInstallation describes a verified installation, not service ownership or
// OS authorization. Installing an App never makes its later processes ours.
type CuaInstallation struct {
	Binary, Bundle, Version string
}

type CuaInstallResult struct {
	Installation      CuaInstallation
	Installed, Reused bool
	Warnings          []string
}

type cuaBundleInstaller struct {
	options      Options
	applications string
	artifact     CuaArtifact
	verify       func(context.Context, string) error
	publish      func(string, string) error
}

func (i cuaBundleInstaller) install(ctx context.Context) (result CuaInstallResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	bundle := filepath.Join(i.applications, "CuaDriver.app")
	installation := CuaInstallation{Bundle: bundle, Binary: filepath.Join(bundle, "Contents", "MacOS", "cua-driver"), Version: CuaDriverVersion}
	// Reuse can be read-only even when helper downloads are disabled. Never
	// trust a same-named PATH binary or replace an unverified existing App.
	if _, err := os.Lstat(bundle); err == nil {
		if err := i.verify(ctx, bundle); err != nil {
			return result, fmt.Errorf("Cua App already exists but is incompatible; existing files were preserved: %w", err)
		}
		return CuaInstallResult{Installation: installation, Reused: true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if i.options.NoInstall {
		return result, errors.New("Cua download disabled by current no_dep_install preference; change applies on next startup")
	}
	// The official public macOS grant flow requires /Applications. Stage and
	// publish there, without sudo, PATH edits, LaunchAgents or login autostart.
	unlock, err := lockCuaInstall(ctx, i.applications)
	if err != nil {
		return result, fmt.Errorf("Cua installation directory is unavailable: %w", err)
	}
	defer func() {
		if err := unlock(); err != nil {
			result.Warnings = append(result.Warnings, "Installation lock cleanup failed: "+err.Error())
		}
	}()
	if _, err := os.Lstat(bundle); err == nil {
		if err := i.verify(ctx, bundle); err != nil {
			return result, fmt.Errorf("another installation appeared and was preserved: %w", err)
		}
		return CuaInstallResult{Installation: installation, Reused: true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	url := strings.TrimRight(i.options.BaseURL, "/") + "/trycua/cua/releases/download/cua-driver-rs-v" + CuaDriverVersion + "/" + i.artifact.Name
	archive, err := download(ctx, i.options, url, i.artifact.SHA256, ".tar.gz")
	if err != nil {
		return result, err
	}
	defer func() {
		if err := os.Remove(archive); err != nil {
			result.Warnings = append(result.Warnings, "Downloaded archive cleanup failed: "+err.Error())
		}
	}()
	stage, err := os.MkdirTemp(i.applications, ".aice-cua-")
	if err != nil {
		return result, err
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			result.Warnings = append(result.Warnings, "Cua staging cleanup failed: "+err.Error())
		}
	}()
	if err := extractCuaBundle(ctx, archive, stage); err != nil {
		return result, err
	}
	stagedBundle := filepath.Join(stage, "CuaDriver.app")
	if err := i.verify(ctx, stagedBundle); err != nil {
		return result, fmt.Errorf("Cua staged App verification failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	licenseDir := filepath.Join(i.options.BinDir, "cua", CuaDriverVersion)
	if err := os.MkdirAll(licenseDir, 0755); err != nil {
		return result, fmt.Errorf("Cua license storage failed before installation: %w", err)
	}
	if err := os.WriteFile(filepath.Join(licenseDir, "LICENSE"), cuaLicense, 0644); err != nil {
		return result, fmt.Errorf("Cua license storage failed before installation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// The exclusive native rename refuses a destination created outside our
	// lock. No replacement/rollback can clobber a user's concurrent installation.
	if err := i.publish(stagedBundle, bundle); err != nil {
		return result, fmt.Errorf("publish Cua App (existing files preserved): %w", err)
	}
	result.Installation, result.Installed = installation, true
	return result, nil
}

func lockCuaInstall(ctx context.Context, directory string) (func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock := filepath.Join(directory, ".aice-cua-install.lock")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := os.Mkdir(lock, 0700)
		if err == nil {
			return func() error { return os.Remove(lock) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// Do not guess ownership or steal a crashed/foreign installer's lock.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("Cua installation lock is busy: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// The pinned full distribution contains a regular-file App, with no symlinks.
// Extract only that App, retaining execute bits and its original signed bytes.
// The bare binary, Node addon and SDK libraries outside it are not installed.
func extractCuaBundle(ctx context.Context, archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	prefix := "cua-driver-rs-" + CuaDriverVersion + "-darwin-universal/"
	seen := make(map[string]bool)
	var total int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if count >= 4096 || header.Size < 0 || header.Size > 256<<20 {
			return errors.New("Cua archive exceeds extraction limits")
		}
		total += header.Size
		if total > 512<<20 {
			return errors.New("Cua archive expands beyond 512 MiB")
		}
		if strings.ContainsAny(header.Name, "\\:") || path.IsAbs(header.Name) || strings.Contains("/"+header.Name, "/../") {
			return errors.New("unsafe Cua archive path")
		}
		name := path.Clean(header.Name)
		if !strings.HasPrefix(name, prefix) && name != strings.TrimSuffix(prefix, "/") {
			return errors.New("unexpected Cua archive root")
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel != "CuaDriver.app" && !strings.HasPrefix(rel, "CuaDriver.app/") {
			continue
		}
		if seen[rel] {
			return errors.New("duplicate Cua archive entry")
		}
		seen[rel] = true
		dest, err := safeJoin(destination, rel)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, &cuaContextReader{ctx: ctx, reader: reader})
			if err := errors.Join(copyErr, out.Close()); err != nil {
				return err
			}
		default:
			return errors.New("Cua App archive contains an unexpected link or special file")
		}
	}
	for _, required := range []string{"Contents/Info.plist", "Contents/_CodeSignature/CodeResources", "Contents/MacOS/cua-driver"} {
		info, err := os.Lstat(filepath.Join(destination, "CuaDriver.app", filepath.FromSlash(required)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Cua archive is missing regular App file %s", required)
		}
	}
	return nil
}

type cuaContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *cuaContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
