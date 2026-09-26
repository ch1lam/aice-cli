package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func inspectLinuxService(ctx context.Context, binary, endpoint string) (Inspection, error) {
	s, err := newLinuxInspector(binary, endpoint)
	if err != nil {
		return Inspection{}, err
	}
	return s.inspect(ctx)
}

func newLinuxInspector(binary, endpoint string) (linuxInspector, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(endpoint) {
		return linuxInspector{}, errors.New("desktop: verified absolute binary and service endpoint required")
	}
	service := &serviceConnector{binary: binary, endpoint: endpoint, status: func(ctx context.Context) (string, error) {
		return serviceCommand(ctx, binary, "status", "--socket", endpoint)
	}}
	return linuxInspector{service: service,
		peer: func(ctx context.Context, pid int) error { return verifyLinuxServicePeer(ctx, binary, endpoint, pid) },
		connect: func(ctx context.Context) (driverClient, error) {
			transport, err := newProcessTransport(binary, endpoint)
			if err != nil {
				return nil, err
			}
			return connectReviewed(ctx, transport, reviewedLinuxStatusTools)
		},
	}, nil
}

// SO_PEERCRED ties the public status PID to the actual listener. /proc verifies
// the executable inode as well as its path, rejecting replaced/deleted binaries.
// Opening and closing this socket sends no Cua request and grants no ownership.
func verifyLinuxServicePeer(ctx context.Context, binary, endpoint string, pid int) error {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return errors.Join(ctx.Err(), serviceError("identity_mismatch", "Cua service peer identity is unavailable"))
	}
	defer connection.Close()
	conn := connection.(*net.UnixConn)
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Ucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credentialErr != nil || credential == nil || int(credential.Pid) != pid || credential.Uid != uint32(os.Geteuid()) {
		return serviceError("identity_mismatch", "Cua service PID or user does not match its listening socket")
	}
	executable := fmt.Sprintf("/proc/%d/exe", pid)
	resolved, err := os.Readlink(executable)
	if err != nil || resolved != filepath.Clean(binary) {
		return serviceError("identity_mismatch", "Cua service executable does not match the verified installation")
	}
	running, err := os.Stat(executable)
	if err != nil {
		return err
	}
	installed, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if !os.SameFile(running, installed) {
		return serviceError("identity_mismatch", "Cua service is running a replaced executable; no reconnection was attempted")
	}
	return ctx.Err()
}
