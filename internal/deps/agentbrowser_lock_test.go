//go:build !windows

package deps

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBrowserReclaimsStaleInstallLockWithConcurrentWaiters(t *testing.T) {
	var hits atomic.Int32
	opts := browserOptions(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte("browser")) })
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(opts.BinDir, "agent-browser.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(command.ProcessState.Pid())), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := Ensure(t.Context(), opts); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("downloads %d", hits.Load())
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("installation lock retained")
	}
}
