//go:build integration && darwin

package desktop

import (
	"context"
	"os"
	"testing"
	"time"
)

// Retire only AICE's owned connection, then explicitly discover again in the
// same run. No capture, input, window activation or daemon restart is performed.
func TestNativeMacSameRunReconnect(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" || os.Getenv("AICE_CUA_NATIVE_RECONNECT") != "1" {
		t.Skip("requires AICE_CUA_NATIVE=1 and AICE_CUA_NATIVE_RECONNECT=1; read-only discovery across an owned connection retirement")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	connector, err := newMacServiceConnector(driver, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	before, err := connector.inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := run.Apps(ctx, "AICE reconnect metadata probe", 1); err != nil {
		t.Fatal("initial discovery", err)
	}
	initialID := run.id
	if err := manager.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Apps(ctx, "AICE reconnect metadata probe", 1); err != nil {
		t.Fatal("same-run discovery after connection retirement", err)
	}
	if manager.Status().Generation != 2 || run.id == initialID {
		t.Fatal("recovery did not establish a new native lifecycle identity")
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := connector.inspect(ctx)
	if err != nil || before.pid != after.pid {
		t.Fatal("recovery changed shared service", err)
	}
	t.Log("same run recovered through a fresh native lifecycle identity; shared service unchanged; no input or capture")
}
