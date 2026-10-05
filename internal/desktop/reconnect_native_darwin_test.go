//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"os"
	"strings"
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
	run, err := bindFixtureRun(manager, ctx, RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := run.discoverApps(ctx, "AICE reconnect metadata probe", 1); err != nil {
		t.Fatal("initial discovery", err)
	}
	initialID := run.id
	if err := manager.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := run.discoverApps(ctx, "AICE reconnect metadata probe", 1); err != nil {
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

// Discovery uses the transport's implicit lifecycle because its pinned public
// schemas do not accept a session argument. Keep only the explicit test session
// active while allowing that implicit lifecycle to reach its real idle timeout.
func TestNativeMacDiscoveryIdleRecovery(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" || os.Getenv("AICE_CUA_NATIVE_DISCOVERY_EXPIRY") != "1" {
		t.Skip("requires AICE_CUA_NATIVE=1 and AICE_CUA_NATIVE_DISCOVERY_EXPIRY=1; waits six minutes without input or capture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
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
	var firstClient *nativeDiscoveryExpiryClient
	dial := manager.dial
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		wrapped := &nativeDiscoveryExpiryClient{driverClient: client}
		if firstClient == nil {
			firstClient = wrapped
		}
		return wrapped, nil
	}
	run, err := bindFixtureRun(manager, ctx, RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := run.discoverApps(ctx, "AICE discovery idle probe", 1); err != nil {
		t.Fatal("initial discovery", err)
	}
	initialID := run.id
	started := time.Now()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for step := 1; step <= 12; step++ {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
		// This is test-only activity on the explicit lifecycle, not a production
		// keepalive and not an attempt to refresh or revive the implicit lifecycle.
		callCtx, release, err := run.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		reply, callErr := run.callLocked(callCtx, "start_session", map[string]any{"session": initialID})
		release()
		var state struct {
			Active  bool `json:"active"`
			Revived bool `json:"revived"`
		}
		if callErr != nil || reply.IsError || json.Unmarshal(reply.Structured, &state) != nil || !state.Active || state.Revived {
			t.Fatal("explicit lifecycle did not remain active", callErr)
		}
		t.Logf("discovery idle=%s; explicit lifecycle active without revival", time.Since(started).Round(time.Second))
	}
	_, discoveryErr := run.discoverApps(ctx, "AICE discovery idle probe", 1)
	if discoveryErr != nil {
		var failure struct {
			Code    string `json:"code"`
			Refusal struct {
				Code string `json:"code"`
			} `json:"refusal"`
		}
		_ = json.Unmarshal(firstClient.lastDiscovery.Structured, &failure)
		// The daemon may reject an ended session before core dispatch, returning
		// only text. Core refusals instead carry the nested refusal.code field.
		// Production recognizes these pinned expiry forms to retire the connection;
		// this diagnostic independently records which native form was returned.
		daemonEnded := false
		for _, text := range firstClient.lastDiscovery.Text {
			if strings.HasPrefix(text, "session '") && strings.Contains(text, "' has ended; tool call 'list_apps' was rejected.") {
				daemonEnded = true
			}
		}
		t.Logf("discovery error: is_error=%t code=%q refusal_code=%q daemon_ended=%t", firstClient.lastDiscovery.IsError, failure.Code, failure.Refusal.Code, daemonEnded)
		if !firstClient.lastDiscovery.IsError || (failure.Code != "session_ended" && failure.Refusal.Code != "session_ended" && !daemonEnded) {
			t.Error("discovery failed without a native session-ended result", discoveryErr)
		}
		if manager.Status().Connected || run.active {
			t.Fatal("expired implicit lifecycle retained connection or active session")
		}
		if _, err := run.discoverApps(ctx, "AICE discovery idle probe", 1); err != nil {
			t.Fatal("read-only recovery after implicit expiry", err)
		}
		if run.id == initialID || manager.Status().Generation != 2 {
			t.Fatal("recovery reused the retired lifecycle")
		}
		t.Log("native discovery returned session_ended; next explicit discovery recovered on a fresh connection without replaying input")
	} else {
		t.Log("native discovery remained usable after six idle minutes; no recovery was necessary")
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := connector.inspect(ctx)
	if err != nil || before.pid != after.pid {
		t.Fatal("idle test changed shared service", err)
	}
}

type nativeDiscoveryExpiryClient struct {
	driverClient
	lastDiscovery Reply
}

func (c *nativeDiscoveryExpiryClient) call(ctx context.Context, name string, args any) (Reply, error) {
	reply, err := c.driverClient.call(ctx, name, args)
	if name == "list_apps" {
		c.lastDiscovery = reply
	}
	return reply, err
}

func (c *nativeDiscoveryExpiryClient) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	return c.driverClient.(managedClient).Tools(ctx)
}
func (c *nativeDiscoveryExpiryClient) ToolGeneration() uint64 {
	return c.driverClient.(managedClient).ToolGeneration()
}
func (c *nativeDiscoveryExpiryClient) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	result, err := c.driverClient.(managedClient).CallChecked(ctx, name, raw, check)
	if name == "list_apps" {
		c.lastDiscovery = managedReply(result)
	}
	return result, err
}
