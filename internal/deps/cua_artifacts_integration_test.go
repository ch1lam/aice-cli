//go:build integration

package deps

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Static cross-platform archive check. No downloaded executable is run, no
// native signature acceptance is inferred, and no user installation is touched.
func TestCuaNativeReleaseArchives(t *testing.T) {
	directory := os.Getenv("AICE_CUA_TEST_ARTIFACTS")
	if directory == "" {
		t.Skip("set AICE_CUA_TEST_ARTIFACTS to a directory containing all four pinned Windows/Linux archives")
	}
	if !filepath.IsAbs(directory) {
		t.Fatal("archive directory must be absolute")
	}
	for _, goos := range []string{"linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(goos+"/"+arch, func(t *testing.T) {
				artifact, err := CuaDriverArtifact(goos, arch)
				if err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(directory, artifact.Name)
				f, err := os.Open(archive)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.New()
				_, err = io.Copy(hash, f)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if fmt.Sprintf("%x", hash.Sum(nil)) != artifact.SHA256 {
					t.Fatal("archive digest mismatch")
				}
				files, err := cuaNativeFiles(goos, arch)
				if err != nil {
					t.Fatal(err)
				}
				stage := t.TempDir()
				if err := extractCuaNative(t.Context(), archive, stage, artifact.Name, files); err != nil {
					t.Fatal(err)
				}
				t.Logf("archive and %d native file digests verified; native execution and OS signature trust not tested", len(files))
			})
		}
	}
}
