package deps

import (
	"archive/tar"
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
	"sync/atomic"
	"testing"
	"time"
)

type cuaRoundTripper func(*http.Request) (*http.Response, error)

func (f cuaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func syntheticCuaArchive(t *testing.T, extra ...tar.Header) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	w := tar.NewWriter(gz)
	prefix := "cua-driver-rs-" + CuaDriverVersion + "-darwin-universal/"
	for _, name := range []string{"CuaDriver.app/Contents/Info.plist", "CuaDriver.app/Contents/_CodeSignature/CodeResources", "CuaDriver.app/Contents/MacOS/cua-driver", "cua_driver_node_runtime.node", "cua-driver"} {
		body := []byte("synthetic " + name)
		if err := w.WriteHeader(&tar.Header{Name: prefix + name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for j := range extra {
		if err := w.WriteHeader(&extra[j]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(w.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func syntheticCuaInstaller(t *testing.T, data []byte) (cuaBundleInstaller, *atomic.Int32) {
	t.Helper()
	count := new(atomic.Int32)
	options := normalize(Options{BinDir: t.TempDir(), BaseURL: "https://release.invalid", Client: &http.Client{Transport: cuaRoundTripper(func(request *http.Request) (*http.Response, error) {
		count.Add(1)
		if !strings.Contains(request.URL.Path, "/cua-driver-rs-v"+CuaDriverVersion+"/") {
			return nil, errors.New("release URL was not pinned")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: make(http.Header)}, nil
	})}})
	return cuaBundleInstaller{options: options, applications: t.TempDir(), artifact: CuaArtifact{Name: "synthetic.tar.gz", SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, verify: func(_ context.Context, path string) error {
		body, err := os.ReadFile(filepath.Join(path, "Contents", "MacOS", "cua-driver"))
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(body, []byte("synthetic ")) {
			return errors.New("bad synthetic binary")
		}
		return nil
	}, publish: os.Rename}, count
}

func TestCuaInstallationStagesVerifiesAndReuses(t *testing.T) {
	t.Parallel()
	i, count := syntheticCuaInstaller(t, syntheticCuaArchive(t))
	var progress []Progress
	i.options.Progress = func(p Progress) error { progress = append(progress, p); return nil }
	result, err := i.install(t.Context())
	if err != nil || !result.Installed || result.Reused {
		t.Fatal(result, err)
	}
	if result.Installation.Bundle != filepath.Join(i.applications, "CuaDriver.app") {
		t.Fatal(result)
	}
	for _, name := range []string{"cua-driver", "cua_driver_node_runtime.node", ".aice-cua-install.lock"} {
		if _, err := os.Lstat(filepath.Join(i.applications, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected installation %s: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(i.applications)
	if len(entries) != 1 {
		t.Fatal("staging artifacts not removed", entries)
	}
	license, err := os.ReadFile(filepath.Join(i.options.BinDir, "cua", CuaDriverVersion, "LICENSE"))
	if err != nil || !bytes.Equal(license, cuaLicense) {
		t.Fatal("license missing", err)
	}
	if len(progress) < 2 || progress[0].Helper != "Cua Driver" || progress[len(progress)-1].Downloaded != progress[0].Total {
		t.Fatal(progress)
	}
	i.options.NoInstall = true
	result, err = i.install(t.Context())
	if err != nil || !result.Reused || result.Installed || count.Load() != 1 {
		t.Fatal(result, err, count.Load())
	}
}

func TestCuaInstallationFailuresPreserveExternalState(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"disabled", "checksum", "signature", "publish", "license", "existing"} {
		t.Run(kind, func(t *testing.T) {
			i, count := syntheticCuaInstaller(t, syntheticCuaArchive(t))
			switch kind {
			case "disabled":
				i.options.NoInstall = true
			case "checksum":
				i.artifact.SHA256 = strings.Repeat("0", 64)
			case "signature":
				i.verify = func(context.Context, string) error { return errors.New("signature rejected") }
			case "publish":
				i.publish = func(string, string) error { return errors.New("destination appeared") }
			case "license":
				if err := os.WriteFile(filepath.Join(i.options.BinDir, "cua"), []byte("peer"), 0600); err != nil {
					t.Fatal(err)
				}
			case "existing":
				if err := os.MkdirAll(filepath.Join(i.applications, "CuaDriver.app"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			result, err := i.install(t.Context())
			if err == nil {
				t.Fatal("failure accepted")
			}
			if result.Installed {
				t.Fatal("failed preparation published an App", result, err)
			}
			if (kind == "disabled" || kind == "existing") && count.Load() != 0 {
				t.Fatal("unnecessary download")
			}
			_, statErr := os.Stat(filepath.Join(i.applications, "CuaDriver.app"))
			if kind == "existing" {
				if statErr != nil {
					t.Fatal("external state removed", statErr)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("failed verification published an App")
			}
			if _, err := os.Stat(filepath.Join(i.applications, ".aice-cua-install.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("lock retained")
			}
		})
	}
}

func TestCuaExtractionRejectsUnsafeArchive(t *testing.T) {
	t.Parallel()
	prefix := "cua-driver-rs-" + CuaDriverVersion + "-darwin-universal/"
	for _, name := range []string{"../escape", prefix + "CuaDriver.app/../../escape", prefix + "CuaDriver.app/Contents/Info.plist", prefix + "CuaDriver.app/link"} {
		t.Run(name, func(t *testing.T) {
			header := tar.Header{Name: name, Mode: 0644, Typeflag: tar.TypeReg}
			if strings.HasSuffix(name, "/link") {
				header.Typeflag = tar.TypeSymlink
				header.Linkname = "/outside"
			}
			data := syntheticCuaArchive(t, header)
			archive := filepath.Join(t.TempDir(), "cua.tar.gz")
			if err := os.WriteFile(archive, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := extractCuaBundle(t.Context(), archive, t.TempDir()); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}

func TestCuaInstallationLockCancellation(t *testing.T) {
	t.Parallel()
	i, count := syntheticCuaInstaller(t, syntheticCuaArchive(t))
	unlocked, err := lockCuaInstall(t.Context(), i.applications)
	if err != nil {
		t.Fatal(err)
	}
	defer unlocked()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := i.install(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if count.Load() != 0 {
		t.Fatal("download started without ownership")
	}
	if _, err := os.Stat(filepath.Join(i.applications, ".aice-cua-install.lock")); err != nil {
		t.Fatal("another owner's lock removed")
	}
}
