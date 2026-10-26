package deps

import (
	"fmt"
	"strconv"
	"strings"
)

// CuaDriverVersion is what new installations download. CuaMinimumVersion is
// the oldest installed Driver setup accepts; newer ones are Cua's to update.
const (
	CuaDriverVersion  = "0.38.0"
	CuaMinimumVersion = "0.38.0"
)

// CuaVersionSupported reports whether a Driver version is at least
// CuaMinimumVersion. Unparsable versions are unsupported.
func CuaVersionSupported(version string) bool {
	have, want := strings.Split(version, "."), strings.Split(CuaMinimumVersion, ".")
	if len(have) != len(want) {
		return false
	}
	for j := range want {
		h, err := strconv.Atoi(have[j])
		if err != nil || h < 0 {
			return false
		}
		if w, _ := strconv.Atoi(want[j]); h != w {
			return h > w
		}
	}
	return true
}

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
		artifact = CuaArtifact{"cua-driver-rs-0.38.0-darwin-universal.tar.gz", "121cf20cafd2cfa69dd720e8967350c6406b22e558be6889de7a1ae3343b6d4b"}
	case "linux/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.38.0-linux-arm64.tar.gz", "ed8a51125ade8807c7717c3dde13d62bd31e6275e6b5e2446cf018d0b01d93b3"}
	case "linux/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.38.0-linux-x86_64.tar.gz", "a305cbd531f94d6413c5db300c33df68e3adbe8b92b9604cf8e645c5f6cb1fa3"}
	case "windows/arm64":
		artifact = CuaArtifact{"cua-driver-rs-0.38.0-windows-arm64.zip", "c442872360cbb819e288b9f30daa7e3d2693d5945495d1400bcd90af5bd16402"}
	case "windows/amd64":
		artifact = CuaArtifact{"cua-driver-rs-0.38.0-windows-x86_64.zip", "997f20114ad3db227a854e1e5a886234f76405b155f4af70605d4b7ca4a436a1"}
	default:
		return CuaArtifact{}, fmt.Errorf("unsupported Cua platform %s/%s", goos, goarch)
	}
	return artifact, nil
}
