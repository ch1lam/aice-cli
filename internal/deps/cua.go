package deps

import "fmt"

const CuaDriverVersion = "0.29.1"

// CuaArtifact identifies an immutable native distribution. macOS deliberately
// uses the full signed App distribution, never the bare-binary archive.
type CuaArtifact struct {
	Name   string
	SHA256 string
}

func CuaDriverArtifact(goos, goarch string) (CuaArtifact, error) {
	var artifact CuaArtifact
	switch goos + "/" + goarch {
	case "darwin/arm64", "darwin/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.29.1-darwin-universal.tar.gz", "ee376d59ef37afac29a10c60c71469ac85fdc8844d1884bd317edd8def29055a"}
	case "linux/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.29.1-linux-arm64.tar.gz", "47c1efa081057c9c1a18e45b20cb7dd0d7d2313520d18ba7d0d35f271005fe19"}
	case "linux/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.29.1-linux-x86_64.tar.gz", "61a0c0f24d6b03e31bb7a73390db875ecf0de2ce53aa435eadb03d70979d79a5"}
	case "windows/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.29.1-windows-arm64.zip", "ec253f1b916b6cc43ea49a3259d009de24ee6d945e195783bfe7ab5e9cccec96"}
	case "windows/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.29.1-windows-x86_64.zip", "679d30dbda901d65a6573f6f4f94aefca8b1eb3bb2ceadb23225dcbe8faa2470"}
	default:
		return CuaArtifact{}, fmt.Errorf("unsupported Cua platform %s/%s", goos, goarch)
	}
	return artifact, nil
}
