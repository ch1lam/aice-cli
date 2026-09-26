package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func inspectWindowsService(ctx context.Context, binary, endpoint string) (Inspection, error) {
	if !filepath.IsAbs(binary) || endpoint != `\\.\pipe\cua-driver` {
		return Inspection{}, errors.New("desktop: verified absolute binary and default local Cua pipe required")
	}
	inspector := windowsInspector{
		service: &serviceConnector{binary: binary, endpoint: endpoint, status: func(ctx context.Context) (string, error) {
			return serviceCommand(ctx, binary, "status", "--socket", endpoint)
		}},
		peer: func(ctx context.Context, pid int) (windowsServicePeer, error) {
			return verifyWindowsServicePeer(ctx, binary, endpoint, pid)
		},
		connect: func(ctx context.Context) (driverClient, error) {
			transport, err := newProcessTransport(binary, endpoint)
			if err != nil {
				return nil, err
			}
			return connectReviewed(ctx, transport, reviewedWindowsStatusTools)
		},
	}
	return inspector.inspect(ctx)
}

// Read the server identity from a local pipe handle, not from its claimed PID.
// Identification-only SQOS prevents this probe from granting impersonation.
// This sends no request and does not acquire ownership of the native service.
func verifyWindowsServicePeer(ctx context.Context, binary, endpoint string, pid int) (windowsServicePeer, error) {
	unknown := func() (windowsServicePeer, error) {
		return windowsServicePeer{}, errors.Join(ctx.Err(), serviceError("identity_mismatch", "Cua Windows service identity could not be verified"))
	}
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return unknown()
	}
	name, err := windows.UTF16PtrFromString(endpoint)
	if err != nil {
		return unknown()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var pipe windows.Handle
	for {
		if ctx.Err() != nil {
			return unknown()
		}
		pipe, err = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			break
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if err != nil {
		return unknown()
	}
	defer windows.CloseHandle(pipe)
	var serverPID uint32
	if windows.GetNamedPipeServerProcessId(pipe, &serverPID) != nil || serverPID != uint32(pid) {
		return unknown()
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, serverPID)
	if err != nil {
		return unknown()
	}
	defer windows.CloseHandle(process)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(process, &created, &exited, &kernel, &user) != nil {
		return unknown()
	}
	path := make([]uint16, 32768)
	length := uint32(len(path))
	if windows.QueryFullProcessImageName(process, 0, &path[0], &length) != nil || length == 0 || length >= uint32(len(path)) {
		return unknown()
	}
	running, err := os.Stat(windows.UTF16ToString(path[:length]))
	if err != nil {
		return unknown()
	}
	installed, err := os.Stat(binary)
	if err != nil || !os.SameFile(running, installed) {
		return unknown()
	}
	var token windows.Token
	if windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token) != nil {
		return unknown()
	}
	defer token.Close()
	owner, err := token.GetTokenUser()
	if err != nil {
		return unknown()
	}
	self, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !owner.User.Sid.Equals(self.User.Sid) {
		return unknown()
	}
	var session, ownSession uint32
	if windows.ProcessIdToSessionId(serverPID, &session) != nil || windows.ProcessIdToSessionId(uint32(os.Getpid()), &ownSession) != nil || session != ownSession {
		return unknown()
	}
	facts := windowsServicePeer{sessionID: session, created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), uiAccess: PermissionUnknown}
	var uiAccess, returned uint32
	if windows.GetTokenInformation(token, windows.TokenUIAccess, (*byte)(unsafe.Pointer(&uiAccess)), uint32(unsafe.Sizeof(uiAccess)), &returned) == nil && returned == uint32(unsafe.Sizeof(uiAccess)) && uiAccess <= 1 {
		facts.uiAccess = permissionState(uiAccess == 1)
	}
	return facts, ctx.Err()
}
