//go:build integration && darwin

package desktop

import (
	"context"
	"os"
	"testing"
	"time"
)

// Kill only the exact stdio child created by this test, after independent
// widget state proves its pending click committed. The shared daemon stays up.
func TestNativeMacProxyCrash(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" || os.Getenv("AICE_CUA_NATIVE_PROXY_CRASH") != "1" {
		t.Skip("requires AICE_CUA_NATIVE=1 and AICE_CUA_NATIVE_PROXY_CRASH=1; kills only the test-owned MCP proxy")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	target := startNativeFixture(t, ctx, buildNativeFixture(t, ctx), "ProxyCrash", false)
	connector, err := newMacServiceConnector(driver, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	before, err := connector.inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := &nativeCancelCounts{polled: make(chan struct{})}
	var child *os.Process
	connector.connect = func(ctx context.Context) (driverClient, error) {
		transport, err := newProcessTransport(driver, endpoint)
		if err != nil {
			return nil, err
		}
		client, err := connect(ctx, transport)
		if err != nil {
			return nil, err
		}
		child = transport.command.Process
		counts.dials.Add(1)
		return &nativeCancelClient{driverClient: client, counts: counts}, nil
	}
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	// Retain the production service admission and occupancy gate while exposing
	// the created child handle for fault injection. Do not replace any replies.
	manager.dial = connector.dial
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	bind := func() *Run {
		run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := run.Close(); err != nil {
				t.Error(err)
			}
		})
		return run
	}
	observe := func(run *Run) Observation {
		discovery, err := run.Windows(ctx, target.name, 16)
		if err != nil {
			t.Fatal(err)
		}
		ref := ""
		for _, window := range discovery.Windows {
			if window.PID == target.pid && window.Title == target.name {
				if ref != "" {
					t.Fatal("ambiguous crash fixture")
				}
				ref = window.Ref
			}
		}
		if ref == "" {
			t.Fatal("exact crash fixture missing")
		}
		observation, err := run.Observe(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
		if err != nil || observation.Image == nil || observation.Degraded {
			t.Fatal("crash fixture capture unavailable", err)
		}
		return observation
	}
	first := bind()
	initial := observe(first)
	if child == nil || child.Pid == before.pid || child.Pid == target.pid {
		t.Fatal("fault injection does not identify a separate owned proxy")
	}
	request := ActRequest{Kind: "click", ObservationRef: initial.Ref, ElementToken: nativeElement(t, initial, "Commit"), Screenshot: true}
	type completion struct {
		result ActResult
		err    error
	}
	done := make(chan completion, 1)
	go func() { result, err := first.Act(ctx, request); done <- completion{result, err} }()
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
	if counts.clickReturned.Load() {
		t.Fatal("native click response already returned; crash precondition not established")
	}
	select {
	case <-done:
		t.Fatal("action completed before crash injection")
	default:
	}
	started := time.Now()
	if err := child.Kill(); err != nil {
		t.Fatal("kill exact test-owned MCP child", err)
	}
	select {
	case completed := <-done:
		if completed.err != nil || !completed.result.Dispatched || completed.result.Outcome != "unknown" || completed.result.Observation != nil {
			t.Fatal("proxy crash discarded or misclassified the pending native action", completed.err, completed.result.Outcome)
		}
	case <-ctx.Done():
		t.Fatal("crashed proxy action did not settle", ctx.Err())
	}
	elapsed := time.Since(started)
	if manager.Status().Connected || counts.dials.Load() != 1 || counts.clicks.Load() != 1 {
		t.Fatal("crash did not retire the connection without retry")
	}
	if result, err := first.Act(ctx, request); err == nil || result.Dispatched {
		t.Fatal("crashed connection retained an execution reference")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next := bind()
	if result, err := next.Act(ctx, request); err == nil || result.Dispatched {
		t.Fatal("old reference crossed into the replacement run")
	}
	_ = observe(next)
	if counts.dials.Load() != 2 || counts.starts.Load() != 2 || counts.clicks.Load() != 1 || manager.Status().Generation != 2 {
		t.Fatal("explicit read-only recovery did not re-admit exactly once")
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	afterReply := readNativeState(t, target)
	settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
	if settled.Commits != 1 || settled.Value != "AICE-314" || settled.Result != "Result: AICE-314" {
		t.Fatal("recovery repeated or altered committed input")
	}
	after, err := connector.inspect(ctx)
	if err != nil || before.pid != after.pid {
		t.Fatal("proxy crash or recovery changed the shared service", err)
	}
	t.Logf("proxy crash settled=%s outcome=unknown commits=1 click_calls=1 connections=2; read-only recovery and unchanged shared service verified", elapsed)
}
