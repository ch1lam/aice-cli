//go:build windows

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/deps"
)

// hostShells mirrors the application's shell resolution from the process
// environment, so this test covers discovery and the WSL fallback together.
func hostShells() BashShells {
	opts := deps.DefaultOptions()
	native, err := deps.FindNativeBash(opts)
	return BashShells{Native: native, NativeErr: err, WSL: deps.WSLBashPaths(opts)}
}

func TestNewBashPrefersNativeAndRejectsUnusableWSL(t *testing.T) {
	root := t.TempDir()
	windows := filepath.Join(root, "Windows")
	wslDir := filepath.Join(windows, "System32")
	nativeDir := filepath.Join(root, "Git", "bin")
	for _, dir := range []string{wslDir, nativeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bash.exe"), []byte("invalid executable fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USERPROFILE", root)
	t.Setenv("SystemRoot", windows)
	t.Setenv("WINDIR", windows)
	t.Setenv("LocalAppData", filepath.Join(root, "Local"))
	t.Setenv("ProgramFiles", filepath.Join(root, "Programs"))
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "Programs x86"))
	t.Setenv("PATH", wslDir+";"+nativeDir)
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	bash, err := NewBash(t.Context(), workspace, hostShells())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(nativeDir, "bash.exe"); bash.shellPath != want {
		t.Fatalf("shellPath = %q, want %q", bash.shellPath, want)
	}
	t.Setenv("PATH", wslDir)
	if _, err := NewBash(t.Context(), workspace, hostShells()); err == nil || !strings.Contains(err.Error(), "winget install Git.Git") {
		t.Fatalf("NewBash() error = %v, want native Bash installation guidance", err)
	}
}
