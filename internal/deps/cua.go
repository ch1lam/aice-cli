package deps

import "fmt"

const CuaDriverVersion = "0.30.4"

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
		artifact = CuaArtifact{"cua-driver-rs-0.30.4-darwin-universal.tar.gz", "9c75a186f89352fb522dc67791575f8c9e8081a38795af2706e103d41fa72be4"}
	case "linux/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.30.4-linux-arm64.tar.gz", "21d00fa2fafe889e48a4e497fba95e6cd03de027753fc8799d5cf0695c30a8a1"}
	case "linux/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.30.4-linux-x86_64.tar.gz", "84445347ceb3039034ce30577b3b7c19a1f0c1f67639423f9da3a71be0418f90"}
	case "windows/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.30.4-windows-arm64.zip", "225926273a0bf962da7cefdd6915144d41f6ff519fa270f6c57c85436ae860ae"}
	case "windows/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.30.4-windows-x86_64.zip", "71ca8dfb3b98edd1e897ec59715269c40610879e03aeff6ae0e2ce709c6900c9"}
	default:
		return CuaArtifact{}, fmt.Errorf("unsupported Cua platform %s/%s", goos, goarch)
	}
	return artifact, nil
}
