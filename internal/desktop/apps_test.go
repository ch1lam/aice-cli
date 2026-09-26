package desktop

import (
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func appFixtureDriver(t *testing.T, mode string) *fakeDriver {
	t.Helper()
	f := &fakeDriver{}
	f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
		switch name {
		case "list_apps":
			return structuredReply(map[string]any{"apps": []any{
				map[string]any{"name": "合成编辑器", "bundle_id": "org.synthetic.editor", "pid": 41, "running": true, "app_ref": "forged-upstream"},
				map[string]any{"name": "Unopened synthetic app", "bundle_id": "org.synthetic.cold", "pid": 0, "running": false},
			}}), nil, true
		case "launch_app":
			if len(args) != 1 || args["bundle_id"] != "org.synthetic.editor" {
				t.Fatal("launch identity or arguments escaped discovery", args)
			}
			if mode == "lost-response" {
				return Reply{}, io.EOF, true
			}
			windows := []any{map[string]any{"pid": 41, "window_id": 99, "title": "Synthetic document"}}
			if mode == "late-window" {
				windows = nil
			}
			if mode == "multiple" {
				windows = append(windows, map[string]any{"pid": 41, "window_id": 101, "title": "Another document"})
			}
			return structuredReply(map[string]any{"pid": 41, "bundle_id": "org.synthetic.editor", "windows": windows, "launch_state": map[string]bool{"requested": true, "process_running": true}}), nil, true
		case "list_windows":
			if mode == "late-window" && f.count("launch_app") > 0 && f.count("list_windows") < 3 {
				return structuredReply(map[string]any{"windows": []any{}}), nil, true
			}
		}
		return Reply{}, nil, false
	}
	return f
}

func TestApplicationDiscoveryFiltersRealIdentitiesAndKeepsInstalledApps(t *testing.T) {
	t.Parallel()
	f := appFixtureDriver(t, "ready")
	_, r := testRun(t, f, false)
	discovery, err := r.Apps(t.Context(), "org.synthetic.editor", 8)
	if err != nil || len(discovery.Apps) != 1 || len(discovery.Windows) != 1 || discovery.Apps[0].Name != "合成编辑器" || discovery.Apps[0].Ref == "forged-upstream" {
		t.Fatal(discovery, err)
	}
	old := discovery.Apps[0].Ref
	discovery, err = r.Apps(t.Context(), "unopened", 8)
	if err != nil || len(discovery.Apps) != 1 || discovery.Apps[0].Running || discovery.Apps[0].Ref == "" {
		t.Fatal(discovery, err)
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "launch", AppRef: old}); err == nil {
		t.Fatal("rediscovery retained old app reference")
	}
	if f.count("launch_app") != 0 || f.count("get_window_state") != 0 {
		t.Fatal("discovery observed content or launched an app")
	}
}

func TestApplicationLaunchNeverRepeatsAndReturnsWindowDecision(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ready", "multiple", "late-window", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := appFixtureDriver(t, mode)
			_, r := testRun(t, f, false)
			discovery, err := r.Apps(t.Context(), "合成", 8)
			if err != nil || len(discovery.Apps) != 1 {
				t.Fatal(discovery, err)
			}
			request := ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref}
			result, err := r.Act(t.Context(), request)
			if err != nil || !result.Dispatched || f.count("launch_app") != 1 {
				t.Fatal(result, err)
			}
			switch mode {
			case "ready", "late-window":
				if result.Outcome != "returned" || result.Observation == nil || len(result.Windows) != 1 || f.count("get_window_state") != 1 {
					t.Fatal("launch did not observe exact ready window", result)
				}
			case "multiple":
				if result.Observation != nil || len(result.Windows) != 2 || result.Diagnostic == "" || f.count("get_window_state") != 0 {
					t.Fatal("multiple windows were silently reduced to first", result)
				}
			case "lost-response":
				if result.Outcome != "unknown" || result.Observation != nil || f.count("list_windows") != 1 {
					t.Fatal("lost launch response was retried or treated as failure", result)
				}
			}
			if _, err := r.Act(t.Context(), request); err == nil || f.count("launch_app") != 1 {
				t.Fatal("consumed launch was repeated")
			}
		})
	}
}

func TestLaunchValidationDoesNotConsumeReference(t *testing.T) {
	t.Parallel()
	f := appFixtureDriver(t, "ready")
	_, r := testRun(t, f, false)
	discovery, err := r.Apps(t.Context(), "editor", 8)
	if err != nil {
		t.Fatal(err)
	}
	ref := discovery.Apps[0].Ref
	if _, err := r.Act(t.Context(), ActRequest{Kind: "launch", AppRef: ref, Text: "unrelated"}); err == nil {
		t.Fatal("launch accepted arbitrary text")
	}
	if f.count("launch_app") != 0 {
		t.Fatal("invalid launch dispatched")
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "launch", AppRef: ref}); err != nil {
		t.Fatal("local validation consumed app reference", err)
	}
}

func TestLaunchWaitNeverUsesAnotherApplicationsWindow(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"target-arrives", "deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				f := appFixtureDriver(t, "late-window")
				f.image = pixelFixture(t)
				base := f.handle
				polls := 0
				f.handle = func(callCtx context.Context, name string, args map[string]any) (Reply, error, bool) {
					if name == "get_window_state" && (args["pid"] != 41 || args["window_id"] != uint64(99)) {
						t.Fatal("launch observed an unrelated window", args)
					}
					if name != "list_windows" || f.count("launch_app") == 0 {
						return base(callCtx, name, args)
					}
					polls++
					// The title matches the intended document; only the process
					// identity distinguishes this already-open foreign window.
					windows := []any{map[string]any{"pid": 55, "window_id": 101, "app_name": "Synthetic editor", "title": "Synthetic document"}}
					if mode == "target-arrives" && polls >= 3 {
						windows = append(windows, map[string]any{"pid": 41, "window_id": 99, "title": "Synthetic document"})
					}
					if mode == "cancel" {
						cancel()
					}
					return structuredReply(map[string]any{"windows": windows}), nil, true
				}
				_, run := testRun(t, f, true)
				discovery, err := run.Apps(t.Context(), "合成", 8)
				if err != nil || len(discovery.Apps) != 1 {
					t.Fatal("initial app discovery failed", err)
				}
				request := ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref, Screenshot: true}
				started := time.Now()
				result, err := run.Act(ctx, request)
				if err != nil || !result.Dispatched || result.Outcome != "returned" || result.DriverError || len(result.Driver) == 0 {
					t.Fatal("window wait discarded the completed launch", result, err)
				}
				if mode == "target-arrives" {
					if len(result.Windows) != 1 || result.Windows[0].PID != 41 || result.Windows[0].WindowID != 99 || result.Observation == nil || result.Observation.Image == nil || result.ObservationError != "" || polls != 3 || f.count("get_window_state") != 1 {
						t.Fatal("late target was not exclusively bound and observed", result, polls)
					}
				} else {
					if len(result.Windows) != 0 || result.Observation != nil || result.ObservationError == "" || f.count("get_window_state") != 0 {
						t.Fatal("failed wait substituted an unrelated window", result)
					}
					if mode == "deadline" && time.Since(started) != 5*time.Second {
						t.Fatal("launch window wait escaped its five-second bound", time.Since(started))
					}
					if mode == "cancel" && (polls != 1 || time.Since(started) != 0) {
						t.Fatal("cancellation did not stop the read-only window wait", polls, time.Since(started))
					}
				}
				if _, err := run.Act(t.Context(), request); err == nil || f.count("launch_app") != 1 {
					t.Fatal("window wait allowed a repeated launch")
				}
				// A cancelled call does not close the run or leave its executor
				// held. Fresh discovery remains available without another launch.
				if _, err := run.Windows(t.Context(), "", 8); err != nil {
					t.Fatal("window wait prevented later discovery", err)
				}
			})
		})
	}
}
