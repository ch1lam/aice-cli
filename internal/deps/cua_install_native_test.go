package deps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type cuaArchiveEntry struct {
	name, body string
	mode       os.FileMode
}

func syntheticCuaNative(t *testing.T, goos string, extra ...cuaArchiveEntry) (cuaNativeInstaller, *atomic.Int32) {
	t.Helper()
	artifact, err := CuaDriverArtifact(goos, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	files, err := cuaNativeFiles(goos, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSuffix(artifact.Name, archiveSuffix(artifact.Name))
	var entries []cuaArchiveEntry
	for name := range files {
		body := "synthetic " + name
		files[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		entries = append(entries, cuaArchiveEntry{root + "/" + name, body, 0755})
	}
	entries = append(entries, cuaArchiveEntry{root + "/SDK-not-installed", "unused", 0644})
	for _, e := range extra {
		e.name = strings.ReplaceAll(e.name, "ROOT", root)
		entries = append(entries, e)
	}
	var data bytes.Buffer
	if goos == "windows" {
		w := zip.NewWriter(&data)
		for _, e := range entries {
			h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			h.SetMode(e.mode)
			f, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(f, e.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&data)
		w := tar.NewWriter(gz)
		for _, e := range entries {
			h := &tar.Header{Name: e.name, Mode: int64(e.mode.Perm()), Size: int64(len(e.body)), Typeflag: tar.TypeReg}
			if e.mode&os.ModeSymlink != 0 {
				h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, "/outside", 0
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Size > 0 {
				if _, err := io.WriteString(w, e.body); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := errors.Join(w.Close(), gz.Close()); err != nil {
			t.Fatal(err)
		}
	}
	artifact.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data.Bytes()))
	count := new(atomic.Int32)
	options := normalize(Options{Goos: goos, Goarch: "amd64", BinDir: t.TempDir(), BaseURL: "https://release.invalid", Client: &http.Client{Transport: cuaRoundTripper(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		if r.URL.Path != "/trycua/cua/releases/download/cua-driver-rs-v"+CuaDriverVersion+"/"+artifact.Name {
			return nil, errors.New("release URL not pinned")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data.Bytes())), ContentLength: int64(data.Len()), Header: make(http.Header)}, nil
	})}})
	return cuaNativeInstaller{options: options, artifact: artifact, files: files, verify: func(context.Context, string) error { return nil }, publish: os.Rename}, count
}

func TestCuaNativeInstallAndReuse(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			i, count := syntheticCuaNative(t, goos)
			var progress []Progress
			i.options.Progress = func(p Progress) error { progress = append(progress, p); return nil }
			result, err := i.install(t.Context())
			if err != nil || !result.Installed || result.Reused || !filepath.IsAbs(result.Installation.Binary) || result.Installation.Bundle != "" {
				t.Fatal(result, err)
			}
			if len(progress) < 2 || progress[0].Helper != "Cua Driver" {
				t.Fatal(progress)
			}
			directory := filepath.Dir(result.Installation.Binary)
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != len(i.files)+1 {
				t.Fatal(entries, err)
			}
			i.options.NoInstall = true
			result, err = i.install(t.Context())
			if err != nil || !result.Reused || result.Installed || count.Load() != 1 {
				t.Fatal(result, err, count.Load())
			}
			// Reuse verifies content without trusting a successful prior installation.
			if err := os.WriteFile(result.Installation.Binary, []byte("changed"), 0755); err != nil {
				t.Fatal(err)
			}
			result, err = i.install(t.Context())
			if err == nil || result.Reused || count.Load() != 1 {
				t.Fatal("modified executable reused", result, err)
			}
			body, err := os.ReadFile(filepath.Join(directory, resultBinary(goos)))
			if err != nil || string(body) != "changed" {
				t.Fatal("existing bytes changed", err)
			}
		})
	}
}

func resultBinary(goos string) string {
	if goos == "windows" {
		return "cua-driver.exe"
	}
	return "cua-driver"
}

func TestCuaNativeInstallFailureCleanup(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"disabled", "checksum", "verification", "cancel", "publish", "concurrent_destination"} {
		t.Run(failure, func(t *testing.T) {
			i, count := syntheticCuaNative(t, "linux")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := filepath.Join(i.options.BinDir, "cua", CuaDriverVersion)
			destination := filepath.Join(root, "linux-amd64")
			switch failure {
			case "disabled":
				i.options.NoInstall = true
			case "checksum":
				i.artifact.SHA256 = strings.Repeat("0", 64)
			case "verification":
				i.verify = func(context.Context, string) error { return errors.New("signature rejected") }
			case "cancel":
				i.verify = func(context.Context, string) error { cancel(); return nil }
			case "publish":
				i.publish = func(string, string) error { return errors.New("publish failed") }
			case "concurrent_destination":
				i.publish = func(_, to string) error {
					if err := os.Mkdir(to, 0755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(to, "peer"), []byte("external"), 0644); err != nil {
						return err
					}
					return os.ErrExist
				}
			}
			result, err := i.install(ctx)
			if err == nil || result.Installed {
				t.Fatal(result, err)
			}
			if failure == "disabled" && count.Load() != 0 {
				t.Fatal("downloaded despite policy")
			}
			entries, readErr := os.ReadDir(root)
			if failure == "disabled" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if failure == "concurrent_destination" {
				body, err := os.ReadFile(filepath.Join(destination, "peer"))
				if err != nil || string(body) != "external" || len(entries) != 1 {
					t.Fatal(entries, err)
				}
			} else if len(entries) != 0 {
				t.Fatal("staging or lock retained", entries)
			}
		})
	}
}

func TestCuaNativeExtractionRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			for _, e := range []cuaArchiveEntry{
				{"ROOT/../escape", "", 0644}, {"ROOT/extra:stream", "", 0644}, {"ROOT/\\escape", "", 0644},
				{"ROOT/" + resultBinary(goos), "replacement", 0755}, {"ROOT/link", "", os.ModeSymlink | 0755}, {"unrelated/file", "", 0644},
			} {
				t.Run(e.name, func(t *testing.T) {
					i, _ := syntheticCuaNative(t, goos, e)
					if _, err := i.install(t.Context()); err == nil {
						t.Fatal("unsafe archive accepted")
					}
				})
			}
		})
	}
}

func TestCuaNativeConcurrentInstall(t *testing.T) {
	t.Parallel()
	i, count := syntheticCuaNative(t, "linux")
	var wg sync.WaitGroup
	var installed, reused atomic.Int32
	for range 2 {
		wg.Go(func() {
			result, err := i.install(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if result.Installed {
				installed.Add(1)
			}
			if result.Reused {
				reused.Add(1)
			}
		})
	}
	wg.Wait()
	if count.Load() != 1 || installed.Load() != 1 || reused.Load() != 1 {
		t.Fatal(count.Load(), installed.Load(), reused.Load())
	}
}

func TestCuaNativeReuseRejectsAdditionalFilesAndLinks(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"additional_library", "missing_helper", "license", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			i, count := syntheticCuaNative(t, "linux")
			result, err := i.install(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Dir(result.Installation.Binary)
			switch kind {
			case "additional_library":
				err = os.WriteFile(filepath.Join(directory, "libcua_driver_sdk.so"), []byte("foreign"), 0644)
			case "missing_helper":
				err = os.Remove(filepath.Join(directory, "cua-cursor-theme"))
			case "license":
				err = os.WriteFile(filepath.Join(directory, "LICENSE"), []byte("changed"), 0644)
			case "symlink":
				original := filepath.Join(t.TempDir(), "original")
				if err := os.Rename(directory, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, directory); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err = i.install(t.Context())
			if err == nil || result.Reused || result.Installed || count.Load() != 1 {
				t.Fatal(result, err, count.Load())
			}
		})
	}
}

func TestCuaRejectedDownloadRemovesTemporaryFile(t *testing.T) {
	// A serial environment-isolated test also runs on native Windows CI,
	// where removing before closing an open file would leak the archive.
	for _, failure := range []string{"checksum", "progress_cancel"} {
		t.Run(failure, func(t *testing.T) {
			i, _ := syntheticCuaNative(t, "windows")
			temporary := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, temporary)
			}
			if failure == "checksum" {
				i.artifact.SHA256 = strings.Repeat("0", 64)
			} else {
				i.options.Progress = func(Progress) error { return context.Canceled }
			}
			_, err := download(t.Context(), i.options, "https://release.invalid/trycua/cua/releases/download/cua-driver-rs-v"+CuaDriverVersion+"/"+i.artifact.Name, i.artifact.SHA256, ".zip")
			if err == nil {
				t.Fatal("invalid download accepted")
			}
			entries, err := os.ReadDir(temporary)
			if err != nil || len(entries) != 0 {
				t.Fatal("download leaked", entries, err)
			}
		})
	}
}
