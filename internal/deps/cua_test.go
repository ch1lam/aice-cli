package deps

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestCuaArtifactsMatchPinnedRelease(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("cua/release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != "d114a50c1487ad20c7f7ca6fb6380284f160fd967d5e2f2050ccbb444e72b52b" {
		t.Fatal("release manifest changed")
	}
	var manifest struct {
		Assets []struct{ Name, SHA256 string }
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, platform := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}, {"windows", "arm64"}, {"windows", "amd64"}} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			artifact, err := CuaDriverArtifact(platform[0], platform[1])
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range manifest.Assets {
				if entry.Name == artifact.Name && entry.SHA256 == artifact.SHA256 {
					return
				}
			}
			t.Fatal("artifact is not in pinned manifest")
		})
	}
	if _, err := CuaDriverArtifact("linux", "386"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
