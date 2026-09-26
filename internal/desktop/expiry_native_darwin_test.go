//go:build integration && darwin

package desktop

import (
	"context"
	"os"
	"testing"
	"time"
)

// Wait for the official daemon's real idle reaper. No private TTL override,
// synthetic expiry response, service restart or native session mutation is used.
func TestNativeMacSessionExpiry(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" || os.Getenv("AICE_CUA_NATIVE_EXPIRY") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 and AICE_CUA_NATIVE_EXPIRY=1; waits for the native five-minute session expiry")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	target := startNativeFixture(t, ctx, binary, "ExpiryTarget", false)
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	calls := make(map[string]int)
	dials := 0
	dial := manager.dial
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		dials++
		return &nativeCountedClient{driverClient: client, calls: calls}, nil
	}
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
					t.Fatal("ambiguous expiry fixture")
				}
				ref = window.Ref
			}
		}
		if ref == "" {
			t.Fatal("exact expiry fixture missing")
		}
		obs, err := run.Observe(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
		if err != nil || obs.Image == nil || obs.Degraded {
			t.Fatal("expiry fixture capture unavailable", err)
		}
		return obs
	}
	first := bind()
	initial := observe(first)
	request := ActRequest{Kind: "click", ObservationRef: initial.Ref, ElementToken: nativeElement(t, initial, "Commit"), Screenshot: true}
	label := first.id
	if len(label) > 28 {
		label = label[:27] + "…"
	}
	if found, _ := nativeCursorSession(t, ctx, driver, endpoint, label); !found {
		t.Fatal("initial native session was not independently visible")
	}
	idleStarted := time.Now()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	expiryCtx, stopExpiry := context.WithTimeout(ctx, 6*time.Minute)
	defer stopExpiry()
	for {
		select {
		case <-expiryCtx.Done():
			t.Fatal("official native session did not expire within six minutes", expiryCtx.Err())
		case <-ticker.C:
		}
		// This separate operator read does not touch the test's native session.
		if found, _ := nativeCursorSession(t, ctx, driver, endpoint, label); !found {
			break
		}
		t.Logf("native session remains active after idle=%s", time.Since(idleStarted).Round(time.Second))
	}
	elapsed := time.Since(idleStarted)
	if elapsed < 290*time.Second {
		t.Fatal("session disappeared before the reviewed default idle expiry", elapsed)
	}
	if calls["start_session"] != 1 || calls["get_window_state"] != 1 || calls["click"] != 0 || dials != 1 {
		t.Fatal("idle wait refreshed or replaced the task session", calls, dials)
	}
	t.Logf("operator confirms native session absent after idle=%s", elapsed.Round(time.Second))
	beforeDispatch := readNativeState(t, target)
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > beforeDispatch.Ticks })
	result, err := first.Act(ctx, request)
	t.Logf("expired action outcome=%s driver_error=%v observation_returned=%v", result.Outcome, result.DriverError, result.Observation != nil)
	if err != nil || !result.Dispatched || (result.Outcome != "unknown" && !(result.Outcome == "returned" && result.DriverError)) || result.Observation != nil {
		t.Fatal("expired action did not preserve a failed or unknown native outcome", err, result.Outcome, result.DriverError)
	}
	afterReply := readNativeState(t, target)
	settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
	if settled.Commits != 0 || settled.Value != "AICE-314" || settled.Result != "Result: pending" {
		t.Fatal("expired session delivered native input")
	}
	if _, err := first.Act(ctx, request); err == nil || calls["click"] != 1 {
		t.Fatal("expired observation was replayed")
	}
	if err := first.Close(); err != nil {
		t.Fatal("expired session cleanup failed", err)
	}
	second := bind()
	if _, err := second.Act(ctx, request); err == nil || calls["click"] != 1 {
		t.Fatal("old observation crossed into the replacement run")
	}
	fresh := observe(second)
	committed, err := second.Act(ctx, ActRequest{Kind: "click", ObservationRef: fresh.Ref, ElementToken: nativeElement(t, fresh, "Commit"), Screenshot: true})
	nativeReturned(t, committed, err)
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
	if calls["start_session"] != 2 || calls["click"] != 2 {
		t.Fatal("expiry recovery revived or replayed a session unexpectedly", calls)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("expiry recovery stopped shared service", err)
	}
	t.Logf("expired outcome=%s driver_error=%v; new-run recovery committed once; connection_count=%d", result.Outcome, result.DriverError, dials)
}
