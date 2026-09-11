package deps

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seedBrowserInstall(t *testing.T, dir string) {
	t.Helper()
	for name, data := range map[string]string{
		"agent-browser":         "existing binary",
		"agent-browser.version": AgentBrowserVersion,
		filepath.Join("agent-browser-skills", AgentBrowserVersion, "core", "SKILL.md"): "name: core",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func browserOptions(t *testing.T, handler http.HandlerFunc) Options {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	opts := DefaultOptions().WithBinDir(t.TempDir())
	opts.Goos, opts.Goarch = "darwin", "arm64"
	opts.BaseURL = server.URL
	opts.LookPath = func(string) (string, error) { return "/system/rg", nil }
	opts.Getenv = func(string) string { return "" }
	opts.Setenv = func(string, string) error { return nil }
	old := agentBrowserSHA256["darwin/arm64"]
	agentBrowserSHA256["darwin/arm64"] = sha256HexBytes([]byte("browser"))
	t.Cleanup(func() { agentBrowserSHA256["darwin/arm64"] = old })
	return opts
}

func TestEnsureBrowserInstall(t *testing.T) {
	var hits atomic.Int32
	opts := browserOptions(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/agent-browser-darwin-arm64") {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("browser"))
	})
	var progress []Progress
	opts.Progress = func(p Progress) error { progress = append(progress, p); return nil }
	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if !AgentBrowserInstalled(opts.BinDir) {
		t.Fatal("incomplete install")
	}
	info, err := os.Stat(filepath.Join(opts.BinDir, "agent-browser"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatal("not executable")
	}
	last := progress[len(progress)-1]
	if last.Helper != "agent-browser" || last.Downloaded != 7 || last.Total != 7 {
		t.Fatalf("progress %+v", progress)
	}
	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("redownloaded installed helper")
	}
	if err := os.WriteFile(filepath.Join(opts.BinDir, "agent-browser.version"), []byte("0.36.0"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatal("did not upgrade")
	}
}

func TestEnsureBrowserFailurePreservesInstall(t *testing.T) {
	opts := browserOptions(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tampered")) })
	seedBrowserInstall(t, opts.BinDir)
	marker := filepath.Join(opts.BinDir, "agent-browser.version")
	if err := os.WriteFile(marker, []byte("0.36.0"), 0644); err != nil {
		t.Fatal(err)
	}
	err := Ensure(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error %v", err)
	}
	version, _ := os.ReadFile(marker)
	binary, _ := os.ReadFile(filepath.Join(opts.BinDir, "agent-browser"))
	if string(version) != "0.36.0" || string(binary) != "existing binary" {
		t.Fatal("old installation changed")
	}
}

func TestEnsureBrowserConcurrent(t *testing.T) {
	var hits atomic.Int32
	opts := browserOptions(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte("browser")) })
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := Ensure(t.Context(), opts); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("downloads %d", hits.Load())
	}
}

func TestEnsureBrowserDisabledAndWindows(t *testing.T) {
	for _, name := range []string{"disabled", "windows"} {
		t.Run(name, func(t *testing.T) {
			opts := browserOptions(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
			var output bytes.Buffer
			opts.Log = &output
			if name == "disabled" {
				opts.Getenv = func(key string) string {
					if key == noInstallEnv {
						return "1"
					}
					return ""
				}
			} else {
				opts.Goos = "windows"
			}
			if err := Ensure(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if name == "disabled" && !strings.Contains(output.String(), "browser automation unavailable") {
				t.Fatal(output.String())
			}
		})
	}
}

func TestBrowserInstallLockCancellation(t *testing.T) {
	opts := normalize(DefaultOptions().WithBinDir(t.TempDir()))
	unlock, err := lockBrowserInstall(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	_, err = lockBrowserInstall(ctx, opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v", err)
	}
}

func TestBrowserReplacementRollback(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "installed")
	newFile := filepath.Join(dir, "new")
	if err := os.WriteFile(dest, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	err := replaceBrowserFiles(dir, []browserReplacement{{from: newFile, to: dest}, {from: filepath.Join(dir, "missing"), to: filepath.Join(dir, "marker")}})
	if err == nil {
		t.Fatal("missing failure")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "old" {
		t.Fatalf("rollback %q", data)
	}
}

func TestBrowserProgressCancellation(t *testing.T) {
	opts := browserOptions(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("browser")) })
	stopped := errors.New("output closed")
	opts.Progress = func(Progress) error { return stopped }
	if err := Ensure(t.Context(), opts); !errors.Is(err, stopped) {
		t.Fatalf("error %v", err)
	}
	if AgentBrowserInstalled(opts.BinDir) {
		t.Fatal("installed cancelled download")
	}
}
