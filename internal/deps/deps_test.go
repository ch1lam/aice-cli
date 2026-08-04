package deps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureIsNoopWhenRipgrepPresent(t *testing.T) {
	binDir := t.TempDir()
	var setPath string
	opts := DefaultOptions().WithBinDir(binDir)
	opts.Goos, opts.Goarch = "darwin", "amd64" // deterministic on every host
	opts.LookPath = func(name string) (string, error) {
		if name == "rg" {
			return "/usr/bin/rg", nil
		}
		return "", errors.New("not found")
	}
	opts.Getenv = func(key string) string {
		if key == "PATH" {
			return "/usr/bin:/bin"
		}
		return ""
	}
	opts.Setenv = func(_ string, value string) error {
		setPath = value
		return nil
	}

	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !strings.HasPrefix(setPath, binDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want bin dir prepended", setPath)
	}
}

func TestEnsureInstallsRipgrepWhenMissing(t *testing.T) {
	fixture := tarGzFixture(t, map[string]string{
		"ripgrep-15.2.0-x86_64-apple-darwin/rg": "fake rg binary",
	})
	old := ripgrepSHA256["darwin/amd64"]
	ripgrepSHA256["darwin/amd64"] = sha256HexBytes(fixture)
	defer func() { ripgrepSHA256["darwin/amd64"] = old }()

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	binDir := t.TempDir()
	var setPath string
	opts := DefaultOptions().WithBinDir(binDir)
	opts.Goos, opts.Goarch = "darwin", "amd64"
	opts.BaseURL = server.URL
	opts.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	opts.Getenv = func(key string) string {
		if key == "PATH" {
			return "/usr/bin:/bin"
		}
		return ""
	}
	opts.Setenv = func(_ string, value string) error {
		setPath = value
		return nil
	}

	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if hits != 1 {
		t.Fatalf("download requests = %d, want 1", hits)
	}
	if info, err := os.Stat(filepath.Join(binDir, "rg")); err != nil || info.IsDir() {
		t.Fatalf("installed rg missing: %v", err)
	}
	if !strings.HasPrefix(setPath, binDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want bin dir prepended", setPath)
	}
}

func TestEnsureRejectsChecksumMismatch(t *testing.T) {
	fixture := tarGzFixture(t, map[string]string{
		"ripgrep-15.2.0-x86_64-apple-darwin/rg": "tampered binary",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	opts := DefaultOptions().WithBinDir(t.TempDir())
	opts.Goos, opts.Goarch = "darwin", "amd64"
	opts.BaseURL = server.URL
	opts.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	opts.Getenv = func(string) string { return "" }
	opts.Setenv = func(string, string) error { return nil }

	err := Ensure(t.Context(), opts)
	if err == nil {
		t.Fatal("Ensure() error = nil, want checksum failure")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Ensure() error = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(opts.BinDir, "rg")); !os.IsNotExist(statErr) {
		t.Fatal("rg installed despite checksum mismatch")
	}
}

func TestEnsureSkipsInstallWhenDisabled(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
	}))
	defer server.Close()

	opts := DefaultOptions().WithBinDir(t.TempDir())
	opts.Goos, opts.Goarch = "darwin", "amd64"
	opts.BaseURL = server.URL
	opts.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	opts.Getenv = func(key string) string {
		if key == noInstallEnv {
			return "1"
		}
		return "/usr/bin"
	}
	opts.Setenv = func(string, string) error { return nil }

	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if hits != 0 {
		t.Fatalf("download requests = %d, want 0 when disabled", hits)
	}
}

// TestDownloadSuffixesTempFile guards the Windows executable requirement: the
// Git Bash self-extractor is run directly, and CreateProcess needs the .exe
// extension on the file it launches.
func TestDownloadSuffixesTempFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fake"))
	}))
	defer server.Close()

	opts := DefaultOptions()
	opts.BaseURL = server.URL
	path, err := download(t.Context(), opts, server.URL+"/asset", sha256HexBytes([]byte("fake")), ".exe")
	if err != nil {
		t.Fatalf("download() error = %v", err)
	}
	defer os.Remove(path)
	if !strings.HasSuffix(path, ".exe") {
		t.Fatalf("download() temp path = %q, want .exe suffix", path)
	}
}

// TestDownloadRejectsOversizedResponse verifies the size cap rejects a response
// larger than the largest legitimate asset.
func TestDownloadRejectsOversizedResponse(t *testing.T) {
	old := maxDownloadBytes
	maxDownloadBytes = 100
	defer func() { maxDownloadBytes = old }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 200))
	}))
	defer server.Close()

	opts := DefaultOptions()
	opts.BaseURL = server.URL
	_, err := download(t.Context(), opts, server.URL+"/asset", "", "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("download() error = %v, want size rejection", err)
	}
}

// TestDownloadHonorsDeadline verifies a stalled download aborts instead of
// blocking startup forever.
func TestDownloadHonorsDeadline(t *testing.T) {
	old := downloadTimeout
	downloadTimeout = 100 * time.Millisecond
	defer func() { downloadTimeout = old }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	opts := DefaultOptions()
	opts.BaseURL = server.URL
	_, err := download(t.Context(), opts, server.URL+"/asset", "", "")
	if err == nil {
		t.Fatal("download() error = nil, want deadline rejection")
	}
}

func TestPrependPath(t *testing.T) {
	got := prependPath([]string{"/a", "/b", "/a"}, "/usr/bin:/b:/bin")
	separator := string(os.PathListSeparator)
	want := "/a" + separator + "/b" + separator + "/usr/bin" + separator + "/bin"
	if got != want {
		t.Fatalf("prependPath() = %q, want %q", got, want)
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	for _, entry := range []string{"../evil", "sub/../../evil", "/absolute"} {
		if _, err := safeJoin("/dest", entry); err == nil {
			t.Fatalf("safeJoin accepted traversal entry %q", entry)
		}
	}
	if path, err := safeJoin("/dest", "a/b.txt"); err != nil || path != filepath.Join("/dest", "a", "b.txt") {
		t.Fatalf("safeJoin() = %q, %v", path, err)
	}
}

func tarGzFixture(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write tar body: %v", err)
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

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
