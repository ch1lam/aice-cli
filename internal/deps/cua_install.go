package deps

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed cua/LICENSE
var cuaLicense []byte

var ErrCuaNotInstalled = errors.New("Cua Driver is not installed")

// CuaInstallation describes a verified installation, not service ownership or
// OS authorization. Bundle is populated only for macOS. Installing a helper
// never makes its later processes ours.
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
	verify       func(context.Context, string) (string, error)
	publish      func(string, string) error
}

func (i cuaBundleInstaller) install(ctx context.Context) (result CuaInstallResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	bundle := filepath.Join(i.applications, "CuaDriver.app")
	installation := CuaInstallation{Bundle: bundle, Binary: filepath.Join(bundle, "Contents", "MacOS", "cua-driver"), Version: CuaDriverVersion}
	// Reuse can be read-only even when helper downloads are disabled. Never
	// trust a same-named PATH binary or replace an existing App. A Cua-signed
	// App of another version is reused: Computer Use talks to it only through
	// MCP, and the shared App may be updated by Cua's own updater.
	if _, err := os.Lstat(bundle); err == nil {
		return i.reuse(ctx, bundle, installation, "Cua App already exists but is incompatible; existing files were preserved")
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if i.options.NoInstall {
		return result, fmt.Errorf("%w; Cua download disabled by current no_dep_install preference; change applies on next startup", ErrCuaNotInstalled)
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
		return i.reuse(ctx, bundle, installation, "another installation appeared and was preserved")
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
	if version, err := i.verify(ctx, stagedBundle); err != nil || version != CuaDriverVersion {
		return result, fmt.Errorf("Cua staged App verification failed: %w", errors.Join(err, errors.New("staged App must report the pinned version")))
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

func (i cuaBundleInstaller) reuse(ctx context.Context, bundle string, installation CuaInstallation, rejected string) (CuaInstallResult, error) {
	version, err := i.verify(ctx, bundle)
	if err != nil {
		return CuaInstallResult{}, fmt.Errorf("%s: %w", rejected, err)
	}
	result := CuaInstallResult{Installation: installation, Reused: true}
	if version != CuaDriverVersion {
		result.Installation.Version = version
		result.Warnings = append(result.Warnings, fmt.Sprintf("Using the installed CuaDriver.app %s; AICE pins %s for new installations", version, CuaDriverVersion))
	}
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

// The pinned full distribution holds only regular files and directories.
// Extract only that App, retaining execute bits and its original signed bytes.
// The bare binary, Node addon and SDK libraries outside it are not installed.
func extractCuaBundle(ctx context.Context, archive, destination string) error {
	root := "cua-driver-rs-" + CuaDriverVersion + "-darwin-universal"
	err := walkArchive(ctx, archive, false, func(entry archiveEntry) error {
		if entry.name != root && !strings.HasPrefix(entry.name, root+"/") {
			return errors.New("unexpected Cua archive root")
		}
		rel := strings.TrimPrefix(entry.name, root+"/")
		if rel != "CuaDriver.app" && !strings.HasPrefix(rel, "CuaDriver.app/") {
			return nil
		}
		target := filepath.Join(destination, filepath.FromSlash(rel))
		if entry.mode.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return extractArchiveFile(ctx, entry, target, entry.mode.Perm()&0o755)
	})
	if err != nil {
		return fmt.Errorf("extract Cua archive: %w", err)
	}
	for _, required := range []string{"Contents/Info.plist", "Contents/_CodeSignature/CodeResources", "Contents/MacOS/cua-driver"} {
		info, err := os.Lstat(filepath.Join(destination, "CuaDriver.app", filepath.FromSlash(required)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Cua archive is missing regular App file %s", required)
		}
	}
	return nil
}
