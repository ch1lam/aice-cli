//go:build integration && darwin

package deps

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in verifies a local release archive and temporary extraction only.
// It does not install /Applications, launch a service, request TCC or capture.
func TestNativeCuaArtifactExtraction(t *testing.T) {
	archive := os.Getenv("AICE_CUA_TEST_ARCHIVE")
	if archive == "" {
		t.Skip("set AICE_CUA_TEST_ARCHIVE to the pinned local macOS archive")
	}
	if !filepath.IsAbs(archive) {
		t.Fatal("archive must be absolute")
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	artifact, _ := CuaDriverArtifact("darwin", "arm64")
	if fmt.Sprintf("%x", hash.Sum(nil)) != artifact.SHA256 {
		t.Fatal("artifact digest mismatch")
	}
	stage := t.TempDir()
	if err := extractCuaBundle(t.Context(), archive, stage); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(stage, "CuaDriver.app")
	if err := verifyCuaApp(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "CuaDriver.app")
	if err := publishCuaApp(app, destination); err != nil {
		t.Fatal(err)
	}
	if err := verifyCuaApp(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "CuaDriver.app")
	if err := os.Mkdir(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := publishCuaApp(other, destination); err == nil {
		t.Fatal("exclusive publish replaced an existing App")
	}
}
