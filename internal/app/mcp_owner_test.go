package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type mcpOwnedFixture struct {
	mcpCatalogFixture
	closed atomic.Int32
	call   func(context.Context) (mcpclient.Result, error)
}

func (f *mcpOwnedFixture) Close() error { f.closed.Add(1); return nil }
func (f *mcpOwnedFixture) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	if f.call != nil {
		f.calls.Add(1)
		return f.call(ctx)
	}
	return f.Call(ctx, name, args)
}

func ownerTestConfig(t *testing.T, count int) config.Config {
	t.Helper()
	root := t.TempDir()
	c := config.Config{Paths: config.Paths{GlobalSettings: filepath.Join(root, "settings.json"), GlobalAuth: filepath.Join(root, "auth.json")}}
	settings := config.MCPSettings{Servers: make(map[string]config.MCPServerSettings)}
	for i := range count {
		settings.Servers[fmt.Sprintf("service%d", i)] = config.MCPServerSettings{Transport: "http", URL: fmt.Sprintf("https://example.com/%d", i)}
	}
	c, err := c.WithMCP(settings)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func ownerTestGuard(t *testing.T) *guard.Guard {
	t.Helper()
	g, err := guard.New("", guard.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func ownerTestApprove(t *testing.T, c config.Config, key string, d config.MCPConnectionDecision) config.Config {
	t.Helper()
	c, err := c.WithMCPConnectionDecision(key, c.MCP.Servers[key].Fingerprint, d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMCPOwnerLazyAndAuthorization(t *testing.T) {
	for _, mode := range []string{"ask", "allow", "deny-yolo", "disabled-yolo", "missing", "yolo"} {
		t.Run(mode, func(t *testing.T) {
			c := ownerTestConfig(t, 1)
			key := "user:service0"
			yolo := mode == "yolo" || mode == "deny-yolo" || mode == "disabled-yolo"
			switch mode {
			case "allow":
				c = ownerTestApprove(t, c, key, config.MCPConnectionAllow)
			case "deny-yolo":
				c = ownerTestApprove(t, c, key, config.MCPConnectionDeny)
			case "disabled-yolo":
				s := c.MCP.Servers[key]
				s.Enabled = false
				c.MCP.Servers[key] = s
			case "missing":
				c = ownerTestApprove(t, c, key, config.MCPConnectionAllow)
				s := c.MCP.Servers[key]
				s.MissingValues = []string{"headers.Authorization"}
				c.MCP.Servers[key] = s
			}
			var opened atomic.Int32
			client := &mcpOwnedFixture{}
			o, err := newMCPOwner(c.MCP, ownerTestGuard(t), yolo, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { opened.Add(1); return client, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			connections := o.Connections()
			o.Status()
			if opened.Load() != 0 {
				t.Fatal("construction/status connected")
			}
			before := connections[key].ToolGeneration()
			result, err := connections[key].CallChecked(t.Context(), "read", json.RawMessage(`{}`), func(context.Context) error { return nil })
			if err == nil || result.State != llm.ExecutionNotDispatched || opened.Load() != 0 {
				t.Fatal("tool call implicitly connected")
			}
			_, err = connections[key].Tools(t.Context())
			allowed := mode == "allow" || mode == "yolo"
			if allowed {
				if err != nil || opened.Load() != 1 || connections[key].ToolGeneration() == before {
					t.Fatalf("discovery: %d %v", opened.Load(), err)
				}
				if _, err = connections[key].Tools(t.Context()); err != nil || opened.Load() != 1 {
					t.Fatal("discovery did not reuse connection")
				}
				if err = o.Close(); err != nil || client.closed.Load() != 1 {
					t.Fatal("owned connection not closed exactly once")
				}
				if connections[key].ToolGeneration() == before+1 {
					t.Fatal("close did not invalidate version")
				}
			} else if err == nil || opened.Load() != 0 {
				t.Fatal("unapproved connection opened")
			}
		})
	}
}

func TestMCPOwnerConcurrentDiscoverySlotsAndRevocation(t *testing.T) {
	c := ownerTestConfig(t, 9)
	var opened atomic.Int32
	o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
		opened.Add(1)
		return &mcpOwnedFixture{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	connections := o.Connections()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { _, _ = connections["user:service0"].Tools(t.Context()) })
	}
	wg.Wait()
	if opened.Load() != 1 {
		t.Fatal("concurrent discovery opened duplicate clients")
	}
	for i := 1; i < 8; i++ {
		if _, err := connections[fmt.Sprintf("user:service%d", i)].Tools(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := connections["user:service8"].Tools(t.Context()); err == nil || opened.Load() != 8 {
		t.Fatal("connection limit not enforced")
	}
	old := connections["user:service0"].ToolGeneration()
	if err := o.Revoke("user:service0"); err != nil {
		t.Fatal(err)
	}
	if connections["user:service0"].ToolGeneration() == old {
		t.Fatal("revocation kept old version")
	}
	if _, err := connections["user:service0"].Tools(t.Context()); err == nil {
		t.Fatal("revoked service reconnected")
	}
	if _, err := connections["user:service8"].Tools(t.Context()); err != nil || opened.Load() != 9 {
		t.Fatal("closed slot was not released")
	}
}

func TestMCPOwnerCloseCancelsOpeningAndClosesLateClient(t *testing.T) {
	c := ownerTestConfig(t, 1)
	started := make(chan struct{})
	released := make(chan struct{})
	client := &mcpOwnedFixture{}
	o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(ctx context.Context, _ mcpclient.Config) (mcpOwnedConnection, error) {
		close(started)
		<-ctx.Done()
		close(released)
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := o.Connections()["user:service0"].Tools(t.Context()); done <- err }()
	<-started
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	<-released
	if err := <-done; err == nil || client.closed.Load() != 1 {
		t.Fatal("late client survived canceled owner")
	}
	if _, err := o.Connections()["user:service0"].Tools(t.Context()); err == nil {
		t.Fatal("closed owner reopened")
	}
}

func TestMCPOwnerRevokeCancelsActionWithoutReplay(t *testing.T) {
	c := ownerTestConfig(t, 1)
	started := make(chan struct{})
	client := &mcpOwnedFixture{}
	client.call = func(ctx context.Context) (mcpclient.Result, error) {
		close(started)
		<-ctx.Done()
		return mcpclient.Result{State: llm.ExecutionUnknown}, ctx.Err()
	}
	var opens atomic.Int32
	o, _ := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { opens.Add(1); return client, nil })
	defer o.Close()
	connection := o.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan mcpclient.Result, 1)
	go func() {
		result, _ := connection.CallChecked(t.Context(), "write", json.RawMessage(`{}`), func(context.Context) error { return nil })
		done <- result
	}()
	<-started
	if err := o.Revoke("user:service0"); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.State != llm.ExecutionUnknown {
		t.Fatal("cancellation lost uncertain execution state")
	}
	result, err := connection.CallChecked(t.Context(), "write", json.RawMessage(`{}`), func(context.Context) error { return nil })
	if err == nil || result.State != llm.ExecutionNotDispatched || opens.Load() != 1 || client.calls.Load() != 1 || client.closed.Load() != 1 {
		t.Fatal("revoked action replayed")
	}
}

func TestMCPOwnerFailedConnectRecoveryAndSafeStatus(t *testing.T) {
	c := ownerTestConfig(t, 1)
	var opens atomic.Int32
	client := &mcpOwnedFixture{}
	o, _ := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
		if opens.Add(1) == 1 {
			return nil, &mcpclient.HTTPError{StatusCode: 401}
		}
		return client, nil
	})
	defer o.Close()
	connection := o.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err == nil {
		t.Fatal("failed connection succeeded")
	}
	if status := o.Status()[0]; status.State != "needs_auth" {
		t.Fatalf("status: %+v", status)
	}
	if _, err := connection.Tools(t.Context()); err != nil || opens.Load() != 2 {
		t.Fatal("explicit later discovery could not retry connect")
	}
	old := connection.ToolGeneration()
	client.generation.Add(1)
	if connection.ToolGeneration() == old {
		t.Fatal("client notification did not invalidate borrowed version")
	}
}

func TestMCPOwnerQueuedCallRechecksAndCancels(t *testing.T) {
	c := ownerTestConfig(t, 1)
	client := &mcpOwnedFixture{}
	entered := make(chan struct{})
	release := make(chan struct{})
	client.list = func(ctx context.Context) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	o, _ := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { return client, nil })
	defer o.Close()
	connection := o.Connections()["user:service0"]
	done := make(chan struct{})
	go func() { defer close(done); _, _ = connection.Tools(t.Context()) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	result, err := connection.CallChecked(ctx, "write", json.RawMessage(`{}`), func(context.Context) error { return errors.New("deny") })
	if err == nil || result.State != llm.ExecutionNotDispatched || client.calls.Load() != 0 {
		t.Fatal("canceled queued call dispatched")
	}
	close(release)
	<-done
	result, err = connection.CallChecked(t.Context(), "write", json.RawMessage(`{}`), func(context.Context) error { return errors.New("new permission") })
	if err == nil || result.State != llm.ExecutionNotDispatched || client.calls.Load() != 0 {
		t.Fatal("final dispatch check skipped")
	}
}
