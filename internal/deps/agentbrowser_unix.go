//go:build !windows

package deps

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Serialize reclaimers on the stale pid inode. Rechecking its identity prevents
// a second waiter from removing a fresh owner's directory after the first reaps it.
func reclaimBrowserInstallLock(dir string) (bool, error) {
	path := filepath.Join(dir, "pid")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return false, err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	original, err := file.Stat()
	if err != nil {
		return false, err
	}
	current, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !os.SameFile(original, current) {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, 32))
	if err != nil {
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 || syscall.Kill(pid, 0) != syscall.ESRCH {
		return false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}
