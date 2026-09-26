package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var errDesktopOccupied = errors.New("desktop occupied")

// lockDesktop coordinates AICE processes, not users or other Cua clients. The
// persistent file is never removed: replacing it could create two lock domains.
// OS ownership ends on unlock/handle close/process exit; file existence is not
// evidence that another process is still active.
func lockDesktop(ctx context.Context, directory string) (func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, ".aice-desktop.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = file.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(serviceError("desktop_busy", "another AICE run is using this desktop; wait for it to finish or stop it before retrying"), err)
		}
		err := tryDesktopLock(file)
		if err == nil {
			accepted = true
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(err, closeDesktopLock(file))
			}
			var once sync.Once
			var releaseErr error
			return func() error {
				once.Do(func() { releaseErr = closeDesktopLock(file) })
				return releaseErr
			}, nil
		}
		if !errors.Is(err, errDesktopOccupied) {
			return nil, fmt.Errorf("desktop: acquire desktop occupancy: %w", err)
		}
		// Bounded wait, interruptible by Stop. No background retry is queued.
		if err := waitNativeProbe(ctx); err != nil {
			return nil, errors.Join(serviceError("desktop_busy", "another AICE run is using this desktop; wait for it to finish or stop it before retrying"), err)
		}
	}
}
