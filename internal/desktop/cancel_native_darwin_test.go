//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Cancels a fixture polling sequence after a completed managed native read,
// with a queued click on another window whose observation is still valid.
// This does not claim cancellation of an in-flight native mutation or TUI Stop.
func TestNativeMacCancelWait(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	waiting := startNativeFixture(t, ctx, binary, "CancelWait", false)
	clicking := startNativeFixture(t, ctx, binary, "CancelClick", false)
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	counts := &nativeCancelCounts{polled: make(chan struct{})}
	dial := manager.dial
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		counts.dials.Add(1)
		return &nativeCancelClient{driverClient: client, counts: counts}, nil
	}
	run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err := run.discoverWindows(ctx, waiting.prefix, 16)
	if err != nil {
		t.Fatal(err)
	}
	find := func(target nativeFixture) string {
		t.Helper()
		ref := ""
		for _, window := range discovery.Windows {
			if window.PID == target.pid && window.Title == target.name {
				if ref != "" {
					t.Fatal("ambiguous synthetic target")
				}
				ref = window.Ref
			}
		}
		if ref == "" {
			t.Fatal("exact synthetic target missing")
		}
		return ref
	}
	waitObs, err := run.observeWindow(ctx, ObserveRequest{TargetRef: find(waiting)})
	if err != nil {
		t.Fatal(err)
	}
	clickObs, err := run.observeWindow(ctx, ObserveRequest{TargetRef: find(clicking), Screenshot: true})
	if err != nil || clickObs.Image == nil {
		t.Fatal("click target observation unavailable", err)
	}
	clickRequest := ActRequest{Kind: "click", ObservationRef: clickObs.Ref, ElementToken: nativeElement(t, clickObs, "Commit"), Screenshot: true}
	type completion struct {
		result ActResult
		err    error
	}
	waitDone, clickDone := make(chan completion, 1), make(chan completion, 1)
	go func() {
		result, err := run.actAndObserve(ctx, ActRequest{Kind: "wait", ObservationRef: waitObs.Ref, Wait: &WaitCondition{Text: "AICE impossible cancellation condition", TimeoutMS: 10000}})
		waitDone <- completion{result, err}
	}()
	select {
	case <-counts.polled:
	case done := <-waitDone:
		t.Fatalf("condition wait ended before a native poll: %v", done.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Hold one dispatch reservation to establish a queued-operation precondition.
	// Polling itself no longer owns the gate between managed MCP calls.
	<-manager.gate
	entered := make(chan struct{})
	go func() {
		close(entered)
		result, err := run.actAndObserve(ctx, clickRequest)
		clickDone <- completion{result, err}
	}()
	<-entered
	select {
	case <-clickDone:
		t.Fatal("competing click escaped the managed dispatch reservation")
	default:
	}
	started := time.Now()
	run.cancel() // Stop invalidates the run before allowing queued dispatch.
	manager.gate <- struct{}{}
	if err := run.Close(); err != nil {
		t.Fatal("native run close failed", err)
	}
	closeElapsed := time.Since(started)
	if closeElapsed > cleanupTimeout+time.Second {
		t.Fatal("native run close exceeded cleanup bound")
	}
	for name, done := range map[string]<-chan completion{"wait": waitDone, "click": clickDone} {
		select {
		case result := <-done:
			if !errors.Is(result.err, context.Canceled) || result.result.Dispatched {
				t.Fatalf("%s did not stop before mutation: dispatched=%v error=%v", name, result.result.Dispatched, result.err)
			}
		case <-ctx.Done():
			t.Fatal("cancelled call did not settle", name)
		}
	}
	if counts.clicks.Load() != 0 || counts.starts.Load() != 1 || counts.ends.Load() != 1 || len(run.observations) != 0 || !manager.Status().Connected {
		t.Fatal("cancel did not invalidate references, end exactly one session and preserve the connection")
	}
	if result, err := run.actAndObserve(ctx, clickRequest); !errors.Is(err, context.Canceled) || result.Dispatched {
		t.Fatal("closed run accepted an old action")
	}
	for _, target := range []nativeFixture{waiting, clicking} {
		after := readNativeState(t, target)
		state := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
		if state.Commits != 0 || state.Value != "AICE-314" || state.Result != "Result: pending" {
			t.Fatal("cancelled task changed a synthetic control")
		}
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("cancel stopped shared service", err)
	}
	// Only a new binding, new discovery and new observation may resume input.
	next, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err = next.discoverWindows(ctx, clicking.prefix, 16)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := next.observeWindow(ctx, ObserveRequest{TargetRef: find(clicking), Screenshot: true})
	if err != nil || fresh.Image == nil {
		t.Fatal("new run observation unavailable", err)
	}
	if result, err := next.actAndObserve(ctx, clickRequest); err == nil || result.Dispatched {
		t.Fatal("new run accepted cancelled run's reference")
	}
	result, err := next.actAndObserve(ctx, ActRequest{Kind: "click", ObservationRef: fresh.Ref, ElementToken: nativeElement(t, fresh, "Commit"), Screenshot: true})
	nativeReturned(t, result, err)
	awaitNativeState(t, ctx, clicking, func(s nativeFixtureState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if counts.dials.Load() != 1 || counts.starts.Load() != 2 || counts.ends.Load() != 2 || counts.clicks.Load() != 1 {
		t.Fatal("resume changed connection/session ownership or replayed input")
	}
	t.Logf("native wait cancellation: close=%s, two sessions on one connection, zero cancelled clicks, one explicit fresh-run commit, shared service preserved", closeElapsed)
}

type nativeCancelCounts struct {
	dials, starts, ends, observations, clicks atomic.Int32
	polled                                    chan struct{}
	clickReturned                             atomic.Bool
}

type nativeCancelClient struct {
	driverClient
	counts *nativeCancelCounts
}

func (c *nativeCancelClient) call(ctx context.Context, name string, args any) (Reply, error) {
	switch name {
	case "start_session":
		c.counts.starts.Add(1)
	case "end_session":
		c.counts.ends.Add(1)
	case "click":
		c.counts.clicks.Add(1)
	}
	reply, err := c.driverClient.call(ctx, name, args)
	if name == "click" && err == nil {
		c.counts.clickReturned.Store(true)
	}
	// Signal only after the real third observation completed successfully:
	// two initial windows, then the condition wait's first semantic poll.
	if name == "get_window_state" && err == nil && !reply.IsError && c.counts.observations.Add(1) == 3 {
		close(c.counts.polled)
	}
	return reply, err
}

// Independent fixture state proves input occurred before cancellation. The
// response must still be pending then; a completed response cannot stand in for
// an in-flight cancellation. Any later complete reply remains a known outcome.
func TestNativeMacCancelDispatchedClick(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens a synthetic window")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	target := startNativeFixture(t, ctx, binary, "CancelDispatched", false)
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	counts := &nativeCancelCounts{polled: make(chan struct{})}
	dial := manager.dial
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		counts.dials.Add(1)
		return &nativeCancelClient{driverClient: client, counts: counts}, nil
	}
	run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err := run.discoverWindows(ctx, target.name, 16)
	if err != nil {
		t.Fatal(err)
	}
	ref := ""
	for _, window := range discovery.Windows {
		if window.PID == target.pid && window.Title == target.name {
			if ref != "" {
				t.Fatal("ambiguous synthetic target")
			}
			ref = window.Ref
		}
	}
	if ref == "" {
		t.Fatal("exact synthetic target missing")
	}
	obs, err := run.observeWindow(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
	if err != nil || obs.Image == nil {
		t.Fatal("native observation unavailable", err)
	}
	request := ActRequest{Kind: "click", ObservationRef: obs.Ref, ElementToken: nativeElement(t, obs, "Commit"), Screenshot: true}
	type completion struct {
		result ActResult
		err    error
	}
	done := make(chan completion, 1)
	go func() { result, err := run.actAndObserve(ctx, request); done <- completion{result, err} }()
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
	select {
	case <-done:
		t.Fatal("native action already completed before cancellation; in-flight precondition not established")
	default:
	}
	if counts.clickReturned.Load() {
		t.Fatal("native RPC already returned; only the post-observation remained in flight")
	}
	started := time.Now()
	if err := run.Close(); err != nil {
		t.Fatal("native run close failed", err)
	}
	elapsed := time.Since(started)
	if elapsed > cleanupTimeout+time.Second {
		t.Fatal("in-flight close exceeded cleanup bound")
	}
	var completed completion
	select {
	case completed = <-done:
	case <-ctx.Done():
		t.Fatal("dispatched action did not settle")
	}
	wantOutcome := "unknown"
	if counts.clickReturned.Load() {
		wantOutcome = "returned"
	}
	if completed.err != nil || !completed.result.Dispatched || completed.result.Outcome != wantOutcome || counts.clicks.Load() != 1 {
		t.Fatalf("cancel discarded or replayed dispatch: dispatched=%v outcome=%s want=%s calls=%d error=%v", completed.result.Dispatched, completed.result.Outcome, wantOutcome, counts.clicks.Load(), completed.err)
	}
	if result, err := run.actAndObserve(ctx, request); !errors.Is(err, context.Canceled) || result.Dispatched {
		t.Fatal("cancelled run accepted another mutation")
	}
	afterReply := readNativeState(t, target)
	settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
	if settled.Commits != 1 || settled.Value != "AICE-314" || settled.Result != "Result: AICE-314" {
		t.Fatal("cancelled native input replayed or altered an unrelated control")
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("in-flight cancellation stopped shared service", err)
	}
	// Recover read-only in a new run. Do not repeat the already committed click.
	next, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err = next.discoverWindows(ctx, target.name, 16)
	if err != nil {
		t.Fatal(err)
	}
	ref = ""
	for _, window := range discovery.Windows {
		if window.PID == target.pid && window.Title == target.name {
			if ref != "" {
				t.Fatal("ambiguous recovery target")
			}
			ref = window.Ref
		}
	}
	if ref == "" {
		t.Fatal("exact recovery target missing")
	}
	fresh, err := next.observeWindow(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
	if err != nil || fresh.Image == nil {
		t.Fatal("fresh recovery observation unavailable", err)
	}
	if result, err := next.actAndObserve(ctx, request); err == nil || result.Dispatched {
		t.Fatal("recovery accepted old execution reference")
	}
	if counts.clicks.Load() != 1 || readNativeState(t, target).Commits != 1 {
		t.Fatal("read-only recovery repeated committed input")
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("native in-flight click cancellation: close=%s outcome=%s commits=1 click_calls=1 dials=%d sessions_started=%d explicit_session_ends=%d, read-only recovery succeeded, shared service preserved", elapsed, completed.result.Outcome, counts.dials.Load(), counts.starts.Load(), counts.ends.Load())
}

func (c *nativeCancelClient) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	return c.driverClient.(managedClient).Tools(ctx)
}
func (c *nativeCancelClient) ToolGeneration() uint64 {
	return c.driverClient.(managedClient).ToolGeneration()
}
func (c *nativeCancelClient) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if name == "click" {
		c.counts.clicks.Add(1)
	}
	result, err := c.driverClient.(managedClient).CallChecked(ctx, name, raw, check)
	if name == "click" && err == nil {
		c.counts.clickReturned.Store(true)
	}
	if name == "get_window_state" && err == nil && !result.IsError && c.counts.observations.Add(1) == 3 {
		close(c.counts.polled)
	}
	return result, err
}
