package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creativeprojects/go-selfupdate"
)

// testUpdater builds a go-selfupdate updater pointed at a local manifest
// server, so tests exercise the real detect/verify/replace pipeline without
// any network access.
func testUpdater(t *testing.T, server *httptest.Server) *selfupdate.Updater {
	t.Helper()
	source, err := selfupdate.NewHttpSource(selfupdate.HttpConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewHttpSource() error = %v", err)
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    source,
		OS:        "linux",
		Arch:      "amd64",
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: checksumsFileName},
	})
	if err != nil {
		t.Fatalf("NewUpdater() error = %v", err)
	}
	return updater
}

func testOptions(updater *selfupdate.Updater) Options {
	return Options{
		Goos:       "linux",
		Goarch:     "amd64",
		Client:     updater,
		Repository: selfupdate.ParseSlug(repositorySlug),
	}
}

type releaseFixture struct {
	tag        string
	prerelease bool
	assetData  []byte
	checksum   string // empty means the real hash of assetData
}

// newReleaseServer serves a manifest.yaml plus the binary and checksums assets
// that go-selfupdate expects. hits counts manifest requests when non-nil.
func newReleaseServer(t *testing.T, rel releaseFixture, hits *int) *httptest.Server {
	t.Helper()
	if rel.tag == "" {
		rel.tag = "v1.2.0"
	}
	if rel.assetData == nil {
		rel.assetData = tarGzBinary(t, "aice", []byte("fake aice binary"))
	}
	if rel.checksum == "" {
		rel.checksum = fmt.Sprintf("%x", sha256.Sum256(rel.assetData))
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		switch r.URL.Path {
		case "/ch1lam/aice-cli/manifest.yaml":
			fmt.Fprintf(w, `last_release_id: 1
last_asset_id: 2
releases:
  - id: 1
    name: %s
    tag_name: %s
    prerelease: %v
    assets:
      - id: 1
        name: aice_linux_amd64.tar.gz
        url: %s/assets/aice_linux_amd64.tar.gz
      - id: 2
        name: checksums.txt
        url: %s/assets/checksums.txt
`, rel.tag, rel.tag, rel.prerelease, server.URL, server.URL)
		case "/assets/aice_linux_amd64.tar.gz":
			_, _ = w.Write(rel.assetData)
		case "/assets/checksums.txt":
			fmt.Fprintf(w, "%s  aice_linux_amd64.tar.gz\n", rel.checksum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCheckReportsLatest(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"

	result, err := Check(t.Context(), opts)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Latest != "1.2.0" || !result.Available {
		t.Fatalf("Check() = %+v, want latest 1.2.0 and available", result)
	}
}

func TestCheckSkipsCurrentVersion(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.0"

	result, err := Check(t.Context(), opts)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Available {
		t.Fatalf("Check() = %+v, want not available", result)
	}
}

func TestCheckIgnoresDevelopmentBuild(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "dev"

	result, err := Check(t.Context(), opts)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Available {
		t.Fatalf("Check() = %+v, want not available for dev build", result)
	}
}

func TestCheckIgnoresPrereleaseReleases(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{
		tag:        "v1.3.0-beta.1",
		prerelease: true,
	}, nil)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.0"

	_, err := Check(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "no release asset") {
		t.Fatalf("Check() error = %v, want no release asset", err)
	}
}

// TestCheckIgnoresMalformedVersion guards against a panic: the semver guard
// must reject identifiers the parser rejects (leading-zero prerelease numbers)
// before any comparison runs.
func TestCheckIgnoresMalformedVersion(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.3-rc.01"

	result, err := Check(t.Context(), opts)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Available {
		t.Fatalf("Check() = %+v, want not available for malformed version", result)
	}
}

func TestUpdateReplacesExecutable(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Executable = func() (string, error) { return exe, nil }

	result, err := Update(t.Context(), opts, false)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.Updated || result.Latest != "1.2.0" {
		t.Fatalf("Update() = %+v, want updated to 1.2.0", result)
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(content) != "fake aice binary" {
		t.Fatalf("executable content = %q, want new binary", content)
	}
	// Windows has no Unix execute-permission bits; executability is derived
	// from the file extension instead, so only assert mode on Unix-like OSes.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(exe); err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("executable mode: %v, %v", err, info.Mode())
		}
	}
}

// TestUpdateIgnoresExtraArchiveFiles mirrors the release layout: an archive
// that also carries LICENSE and README.md must still replace the executable
// found inside it.
func TestUpdateIgnoresExtraArchiveFiles(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{
		assetData: tarGzFiles(t, map[string]string{
			"aice":      "fake aice binary",
			"LICENSE":   "Apache-2.0",
			"README.md": "# AICE",
		}),
	}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Executable = func() (string, error) { return exe, nil }

	if _, err := Update(t.Context(), opts, false); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(content) != "fake aice binary" {
		t.Fatalf("executable content = %q, want new binary", content)
	}
}

func TestUpdateSkipsWhenUpToDate(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.0"
	opts.Executable = func() (string, error) { return exe, nil }

	result, err := Update(t.Context(), opts, false)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.Updated {
		t.Fatalf("Update() = %+v, want not updated", result)
	}
	content, _ := os.ReadFile(exe)
	if string(content) != "old binary" {
		t.Fatalf("executable changed to %q, want untouched", content)
	}
}

func TestUpdateRequiresForceForUnknownVersion(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "dev"
	opts.Executable = func() (string, error) { return exe, nil }

	if _, err := Update(t.Context(), opts, false); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("Update() error = %v, want --force hint", err)
	}
	result, err := Update(t.Context(), opts, true)
	if err != nil {
		t.Fatalf("Update(--force) error = %v", err)
	}
	if !result.Updated {
		t.Fatalf("Update(--force) = %+v, want updated", result)
	}
}

func TestUpdateRejectsMalformedVersion(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.3-rc.01"
	opts.Executable = func() (string, error) { return exe, nil }

	if _, err := Update(t.Context(), opts, false); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("Update() error = %v, want --force hint", err)
	}
	content, _ := os.ReadFile(exe)
	if string(content) != "old binary" {
		t.Fatalf("executable changed to %q after rejected update", content)
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{
		checksum: strings.Repeat("0", 64),
	}, nil)
	exe := writeExecutable(t, "old binary")
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Executable = func() (string, error) { return exe, nil }

	_, err := Update(t.Context(), opts, false)
	if !errors.Is(err, selfupdate.ErrChecksumValidationFailed) {
		t.Fatalf("Update() error = %v, want checksum failure", err)
	}
	content, _ := os.ReadFile(exe)
	if string(content) != "old binary" {
		t.Fatalf("executable changed to %q after failed update", content)
	}
}

func TestUpdateRejectsPackageManagerInstall(t *testing.T) {
	opts := Options{
		Current: "1.1.0",
		Executable: func() (string, error) {
			return "/opt/homebrew/Cellar/aice/1.1.0/bin/aice", nil
		},
	}

	_, err := Update(t.Context(), opts, false)
	if err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("Update() error = %v, want Homebrew hint", err)
	}
}

func TestNotifyPrintsUpdateAvailable(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	var stderr bytes.Buffer
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Getenv = func(string) string { return "" }
	opts.IsTerminal = func() bool { return true }
	opts.StatePath = filepath.Join(t.TempDir(), "update-state")
	opts.Stderr = &stderr
	opts.Now = time.Now

	Notify(t.Context(), opts)
	if !strings.Contains(stderr.String(), "update available") {
		t.Fatalf("stderr = %q, want update hint", stderr.String())
	}
	if _, err := os.Stat(opts.StatePath); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
}

func TestNotifySkipsWhenDisabledByEnvironment(t *testing.T) {
	var hits int
	server := newReleaseServer(t, releaseFixture{}, &hits)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Getenv = func(key string) string {
		if key == noCheckEnv {
			return "1"
		}
		return ""
	}
	opts.IsTerminal = func() bool { return true }

	Notify(t.Context(), opts)
	if hits != 0 {
		t.Fatalf("manifest requests = %d, want 0 when disabled", hits)
	}
}

func TestNotifySkipsOutsideTerminal(t *testing.T) {
	var hits int
	server := newReleaseServer(t, releaseFixture{}, &hits)
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Getenv = func(string) string { return "" }
	opts.IsTerminal = func() bool { return false }

	Notify(t.Context(), opts)
	if hits != 0 {
		t.Fatalf("manifest requests = %d, want 0 outside terminal", hits)
	}
}

func TestNotifySkipsWithinFreshCache(t *testing.T) {
	var hits int
	server := newReleaseServer(t, releaseFixture{}, &hits)
	now := time.Now()
	statePath := filepath.Join(t.TempDir(), "update-state")
	if err := os.WriteFile(
		statePath,
		[]byte(fmt.Sprintf(`{"checked_at":%q}`, now.Format(time.RFC3339))),
		0o600,
	); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.1.0"
	opts.Getenv = func(string) string { return "" }
	opts.IsTerminal = func() bool { return true }
	opts.StatePath = statePath
	opts.Now = func() time.Time { return now }

	Notify(t.Context(), opts)
	if hits != 0 {
		t.Fatalf("manifest requests = %d, want 0 within fresh cache", hits)
	}
}

// TestNotifyCachesUpToDateResult guards against re-checking on every launch:
// a completed check must write the state file even when the install is
// already current.
func TestNotifyCachesUpToDateResult(t *testing.T) {
	var hits int
	server := newReleaseServer(t, releaseFixture{}, &hits)
	var stderr bytes.Buffer
	opts := testOptions(testUpdater(t, server))
	opts.Current = "1.2.0"
	opts.Getenv = func(string) string { return "" }
	opts.IsTerminal = func() bool { return true }
	opts.StatePath = filepath.Join(t.TempDir(), "update-state")
	opts.Stderr = &stderr
	opts.Now = time.Now

	Notify(t.Context(), opts)
	if hits != 1 {
		t.Fatalf("manifest requests = %d after first check, want 1", hits)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no output when up to date", stderr.String())
	}
	Notify(t.Context(), opts)
	if hits != 1 {
		t.Fatalf("manifest requests = %d after second check, want 1 (cached)", hits)
	}
}

func TestNotifySilentForDevelopmentBuild(t *testing.T) {
	server := newReleaseServer(t, releaseFixture{}, nil)
	var stderr bytes.Buffer
	opts := testOptions(testUpdater(t, server))
	opts.Current = "dev"
	opts.Getenv = func(string) string { return "" }
	opts.IsTerminal = func() bool { return true }
	opts.Stderr = &stderr
	opts.Now = time.Now

	Notify(t.Context(), opts)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no output for dev build", stderr.String())
	}
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aice")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}

func tarGzBinary(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	return tarGzFiles(t, map[string]string{name: string(content)})
}

func tarGzFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("write tar header for %s: %v", name, err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body for %s: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}
