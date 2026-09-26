package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Ordinary Windows tests use only a pipe owned by this test process. No Cua,
// display, signature grant, installation or elevated process is required.
func TestWindowsPeerRequiresExactExecutableAndPID(t *testing.T) {
	t.Parallel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"self", "wrong-pid", "wrong-executable", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			endpoint := `\\.\pipe\aice-status-test-` + rand.Text()
			name, err := windows.UTF16PtrFromString(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
				windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(pipe)
			event, err := windows.CreateEvent(nil, 1, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(event)
			overlapped := windows.Overlapped{HEvent: event}
			if err := windows.ConnectNamedPipe(pipe, &overlapped); err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
				t.Fatal(err)
			}
			defer func() {
				_ = windows.CancelIoEx(pipe, &overlapped)
				var transferred uint32
				_ = windows.GetOverlappedResult(pipe, &overlapped, &transferred, true)
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			path, pid := binary, os.Getpid()
			switch mode {
			case "wrong-pid":
				pid++
			case "wrong-executable":
				path = filepath.Join(t.TempDir(), "different.exe")
				if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			facts, err := verifyWindowsServicePeer(ctx, path, endpoint, pid)
			if mode != "self" {
				if !serviceHasCode(err, "identity_mismatch") {
					t.Fatal("invalid peer accepted", err)
				}
				if mode == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancel cause lost", err)
				}
				return
			}
			var ownSession uint32
			if err != nil || windows.ProcessIdToSessionId(uint32(os.Getpid()), &ownSession) != nil || facts.sessionID != ownSession || facts.created == 0 {
				t.Fatal("test process identity failed", facts, err)
			}
		})
	}
}
