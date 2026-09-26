//go:build linux || windows

package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCuaNativePublishDoesNotReplace(t *testing.T) {
	t.Parallel()
	for _, populated := range []bool{false, true} {
		root := t.TempDir()
		from, to := filepath.Join(root, "staged"), filepath.Join(root, "installed")
		if err := os.Mkdir(from, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(from, "ours"), []byte("ours"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(to, 0755); err != nil {
			t.Fatal(err)
		}
		if populated {
			if err := os.WriteFile(filepath.Join(to, "peer"), []byte("peer"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := publishCuaNative(from, to); err == nil {
			t.Fatal("replaced existing directory")
		}
		if _, err := os.Stat(filepath.Join(from, "ours")); err != nil {
			t.Fatal("source lost", err)
		}
		if populated {
			body, err := os.ReadFile(filepath.Join(to, "peer"))
			if err != nil || string(body) != "peer" {
				t.Fatal("peer lost", err)
			}
		}
		fresh := filepath.Join(root, "fresh")
		if err := publishCuaNative(from, fresh); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(fresh, "ours")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCuaNativeInstallerRejectsOtherPlatform(t *testing.T) {
	t.Parallel()
	other := "linux"
	if runtime.GOOS == other {
		other = "windows"
	}
	_, err := InstallCua(t.Context(), Options{Goos: other, BinDir: t.TempDir(), NoInstall: true})
	if err == nil {
		t.Fatal("foreign platform accepted")
	}
}
