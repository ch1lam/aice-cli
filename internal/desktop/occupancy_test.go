//go:build unix || windows

package desktop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func assertDesktopBusy(t *testing.T, directory string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	unlock, err := lockDesktop(ctx, directory)
	if unlock != nil {
		_ = unlock()
		t.Fatal("occupied desktop lock acquired")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !serviceHasCode(err, "desktop_busy") {
		t.Fatal("occupancy did not preserve structured busy/cancellation", err)
	}
}

func TestDesktopOccupancyCancellationAndReuse(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unlock() })
	assertDesktopBusy(t, directory)
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal("repeat release failed", err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".aice-desktop.lock")); err != nil {
		t.Fatal("coordination inode removed", err)
	}
	second, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal("released lock could not be reused", err)
	}
	defer second()
}

func TestDesktopOccupancyChild(t *testing.T) {
	directory := os.Getenv("AICE_CUA_LOCK_HELPER_DIR")
	if directory == "" {
		return
	}
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Fprintln(os.Stdout, "locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestDesktopOccupancyReleasedAfterProcessExit(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDesktopOccupancyChild$")
	child.Env = append(driverEnvironment(os.Environ()), "AICE_CUA_LOCK_HELPER_DIR="+directory)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatal("helper did not acquire lock", line)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertDesktopBusy(t, directory)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if ctx.Err() != nil {
		t.Fatal("watchdog expiry is not process-exit evidence", ctx.Err())
	}
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal("dead process retained occupancy", err)
	}
	defer unlock()
}

func occupiedManager(t *testing.T, directory string, f *fakeDriver) *Manager {
	t.Helper()
	m := newManager(func(context.Context) (driverClient, error) { return f, nil })
	m.occupy = func(ctx context.Context) (func() error, error) { return lockDesktop(ctx, directory) }
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func occupancyRun(t *testing.T, m *Manager) *Run {
	t.Helper()
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestDesktopOccupancyFollowsRunsAcrossConnectionRecovery(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	f1, f2 := &fakeDriver{}, &fakeDriver{}
	m1, m2 := occupiedManager(t, directory, f1), occupiedManager(t, directory, f2)
	r1, sibling, r2 := occupancyRun(t, m1), occupancyRun(t, m1), occupancyRun(t, m2)
	if _, err := os.Stat(filepath.Join(directory, ".aice-desktop.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("binding acquired occupancy", err)
	}
	_ = observed(t, r1, false)
	_ = observed(t, sibling, false)
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}
	assertDesktopBusy(t, directory)
	if err := m1.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := r2.Windows(ctx, "", 1); !serviceHasCode(err, "desktop_busy") || len(f2.calls) != 0 {
		t.Fatal("other manager reached native service during recovery", err)
	}
	_ = observed(t, sibling, false)
	if err := sibling.Close(); err != nil {
		t.Fatal(err)
	}
	if !m1.Status().Connected {
		t.Fatal("run completion discarded reusable connection")
	}
	_ = observed(t, r2, false)
	if err := m2.Close(); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal("manager shutdown retained occupancy", err)
	}
	defer unlock()
}

func TestDesktopLateCancelledCallReleasesOccupancy(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	entered, settle := make(chan struct{}), make(chan struct{})
	f := &fakeDriver{handle: func(ctx context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name != "click" {
			return Reply{}, nil, false
		}
		close(entered)
		<-settle // simulate an in-flight native request slow to acknowledge Stop
		return Reply{}, ctx.Err(), true
	}}
	m := occupiedManager(t, directory, f)
	r := occupancyRun(t, m)
	o := observed(t, r, false)
	done := make(chan ActResult, 1)
	go func() {
		result, _ := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token})
		done <- result
	}()
	defer func() {
		select {
		case <-settle:
		default:
			close(settle)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("action did not enter")
	}
	if err := r.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("slow native completion was reported as stopped", err)
	}
	assertDesktopBusy(t, directory)
	close(settle)
	select {
	case result := <-done:
		if result.Outcome != "unknown" || f.count("click") != 1 {
			t.Fatal("cancelled mutation lost uncertainty or replayed", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late cleanup did not finish")
	}
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal("late call leaked occupancy", err)
	}
	defer unlock()
}

func TestDesktopSetupRespectsOtherInstanceOccupancy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS setup integration")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, "Library", "Caches", "cua-driver")
	unlock, err := lockDesktop(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	result, err := Setup(ctx, "/Applications/CuaDriver.app/Contents/MacOS/cua-driver", filepath.Join(directory, "cua-driver.sock"))
	if !serviceHasCode(err, "desktop_busy") || result.LaunchRequested || result.AuthorizationRequested {
		t.Fatal("setup reached native operations while occupied", result, err)
	}
	resolved := false
	m, err := NewManager(func(context.Context) (string, string, error) {
		resolved = true
		return "", "", errors.New("unexpected runtime resolution")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r := occupancyRun(t, m)
	callCtx, callCancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer callCancel()
	if _, err := r.Windows(callCtx, "", 1); !serviceHasCode(err, "desktop_busy") || resolved {
		t.Fatal("public manager resolved runtime before occupancy", err)
	}
}
