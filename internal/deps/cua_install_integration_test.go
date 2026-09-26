//go:build integration && (linux || windows)

package deps

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Exercise the actual native verifier and exclusive publisher in a private
// directory. The supplied archive is still checked against the production pin.
// Only --version and (on Windows) Authenticode verification execute; no daemon,
// autostart, permissions, capture or user installation is involved.
func TestNativeCuaPrivateInstallation(t *testing.T) {
	archive := os.Getenv("AICE_CUA_TEST_NATIVE_ARCHIVE")
	if archive == "" {
		t.Skip("set AICE_CUA_TEST_NATIVE_ARCHIVE to the pinned local archive for this OS and architecture")
	}
	if !filepath.IsAbs(archive) {
		t.Fatal("archive must be absolute")
	}
	artifact, err := CuaDriverArtifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	downloads := 0
	options := DefaultOptions().WithBinDir(t.TempDir())
	options.Client = &http.Client{Transport: cuaRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/trycua/cua/releases/download/cua-driver-rs-v"+CuaDriverVersion+"/"+artifact.Name {
			return nil, errors.New("unexpected release URL")
		}
		file, err := os.Open(archive)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		downloads++
		return &http.Response{StatusCode: http.StatusOK, Body: file, ContentLength: info.Size(), Header: make(http.Header)}, nil
	})}
	installed, err := InstallCua(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || installed.Reused || len(installed.Warnings) != 0 {
		t.Fatal(installed)
	}
	reused, err := InstallCua(t.Context(), options.WithNoInstall(true))
	if err != nil || !reused.Reused || reused.Installed || reused.Installation != installed.Installation || downloads != 1 {
		t.Fatal(reused, err, downloads)
	}
	root := filepath.Dir(filepath.Dir(installed.Installation.Binary))
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("installation left staging or lock files", entries, err)
	}
	t.Logf("native %s/%s: pinned install, native verification and read-only reuse passed; no desktop readiness claimed", runtime.GOOS, runtime.GOARCH)
}
