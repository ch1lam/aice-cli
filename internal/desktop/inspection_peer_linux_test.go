package desktop

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPeerRequiresExactExecutableAndPID(t *testing.T) {
	t.Parallel()
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	binary, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxServicePeer(t.Context(), binary, endpoint, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxServicePeer(t.Context(), binary, endpoint, os.Getpid()+1); !serviceHasCode(err, "identity_mismatch") {
		t.Fatal("foreign pid accepted", err)
	}
	if err := verifyLinuxServicePeer(t.Context(), "/different/driver", endpoint, os.Getpid()); !serviceHasCode(err, "identity_mismatch") {
		t.Fatal("foreign executable accepted", err)
	}
}
