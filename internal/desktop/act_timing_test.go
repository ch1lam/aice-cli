package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func TestActionTimingSeparatesQueueMutationAndObservation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"returned", "lost-response", "observation-failure", "invalid", "cancelled-queue"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeDriver{}
				m, run := testRun(t, f, false)
				observation := observed(t, run, false)
				f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
					switch name {
					case "click":
						time.Sleep(5 * time.Millisecond)
						if mode == "lost-response" {
							return Reply{}, io.EOF, true
						}
					case "get_window_state":
						time.Sleep(7 * time.Millisecond)
						if mode == "observation-failure" {
							return Reply{}, io.EOF, true
						}
					}
					return Reply{}, nil, false
				}
				request := ActRequest{Kind: "click", ObservationRef: observation.Ref, ElementToken: observation.Elements[0].Token}
				if mode == "invalid" {
					request.Text = "unrelated synthetic input"
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				<-m.gate
				var result ActResult
				var err error
				done := make(chan struct{})
				go func() {
					defer close(done)
					result, err = run.Act(ctx, request)
				}()
				synctest.Wait()
				time.Sleep(11 * time.Millisecond)
				if mode == "cancelled-queue" {
					cancel()
					<-done
				}
				m.gate <- struct{}{}
				<-done
				want := ActionTiming{Queue: 11 * time.Millisecond}
				if mode == "invalid" || mode == "cancelled-queue" {
					if err == nil || result.Dispatched || f.count("click") != 0 {
						t.Fatal("rejected action was dispatched", err)
					}
				} else {
					want.Driver = 5 * time.Millisecond
					if mode != "lost-response" {
						want.Observation = 7 * time.Millisecond
					}
					if err != nil || !result.Dispatched || f.count("click") != 1 {
						t.Fatal("mutation facts changed", err)
					}
					if mode == "lost-response" && (result.Outcome != "unknown" || result.Observation != nil) {
						t.Fatal("timing lost unknown-outcome evidence")
					}
					if mode == "observation-failure" && (result.Outcome != "returned" || result.ObservationError == "") {
						t.Fatal("timing lost completed-action evidence")
					}
				}
				want.Total = want.Queue + want.Driver + want.Observation
				if result.Timing != want {
					t.Fatalf("timing=%+v want=%+v", result.Timing, want)
				}
				withTiming, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				result.Timing = ActionTiming{}
				withoutTiming, err := json.Marshal(result)
				if err != nil || !bytes.Equal(withTiming, withoutTiming) {
					t.Fatal("local timings changed model/Session JSON", err)
				}
			})
		})
	}
}

func TestActionTimingSeparatesConditionPollingAndFinalCapture(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"satisfied", "deadline", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeDriver{image: pixelFixture(t)}
				_, run := testRun(t, f, true)
				observation := observed(t, run, false)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				polls := 0
				f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
					if name != "get_window_state" {
						return Reply{}, nil, false
					}
					if args["include_screenshot"] == true {
						time.Sleep(7 * time.Millisecond)
						return Reply{}, nil, false
					}
					time.Sleep(5 * time.Millisecond)
					polls++
					value := "pending"
					if mode == "satisfied" && polls == 2 {
						value = "synthetic"
					}
					if mode == "cancelled" {
						cancel()
					}
					return structuredReply(map[string]any{"pid": 41, "window_id": 99, "snapshot_id": "fresh", "elements": []any{map[string]any{"value": value}}}), nil, true
				}
				result, err := run.Act(ctx, ActRequest{Kind: "wait", ObservationRef: observation.Ref, Wait: &WaitCondition{Text: "synthetic", TimeoutMS: 300}, Screenshot: true})
				want := ActionTiming{ConditionWait: 210 * time.Millisecond, Observation: 7 * time.Millisecond}
				switch mode {
				case "satisfied":
					if err != nil || result.WaitState != "satisfied" || result.Observation == nil || result.Observation.Image == nil {
						t.Fatal("condition did not return final capture", err)
					}
				case "deadline":
					want.ConditionWait, want.Observation = 300*time.Millisecond, 0
					if err != nil || result.WaitState != "unknown" || result.Diagnostic == "" {
						t.Fatal("deadline evidence changed", err)
					}
				case "cancelled":
					want.ConditionWait, want.Observation = 5*time.Millisecond, 0
					if err == nil {
						t.Fatal("cancelled condition ignored cancellation")
					}
				}
				want.Total = want.ConditionWait + want.Observation
				if result.Timing != want || result.Dispatched {
					t.Fatalf("timing=%+v want=%+v dispatched=%v", result.Timing, want, result.Dispatched)
				}
			})
		})
	}
}

func TestActionTimingSeparatesLaunchAndWindowWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := appFixtureDriver(t, "late-window")
		_, run := testRun(t, f, false)
		discovery, err := run.Apps(t.Context(), "合成", 8)
		if err != nil || len(discovery.Apps) != 1 {
			t.Fatal("app discovery failed", err)
		}
		base := f.handle
		f.handle = func(ctx context.Context, name string, args map[string]any) (Reply, error, bool) {
			switch name {
			case "launch_app":
				time.Sleep(3 * time.Millisecond)
			case "list_windows":
				time.Sleep(5 * time.Millisecond)
			case "get_window_state":
				time.Sleep(7 * time.Millisecond)
			}
			return base(ctx, name, args)
		}
		result, err := run.Act(t.Context(), ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref})
		want := ActionTiming{Driver: 3 * time.Millisecond, ConditionWait: 210 * time.Millisecond, Observation: 7 * time.Millisecond, Total: 220 * time.Millisecond}
		if err != nil || result.Observation == nil || result.Timing != want || f.count("launch_app") != 1 {
			t.Fatalf("launch timing=%+v want=%+v err=%v", result.Timing, want, err)
		}
	})
}
