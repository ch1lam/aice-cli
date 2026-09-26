package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Application struct {
	Ref      string `json:"app_ref,omitempty"`
	Name     string `json:"name"`
	BundleID string `json:"bundle_id,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Running  bool   `json:"running"`
}

// Apps discovers installed/running app identities and window metadata. It does
// not read window contents. Query and limits apply independently to each list.
func (r *Run) Apps(ctx context.Context, query string, limit int) (Discovery, error) {
	if len(query) > 256 || limit < 1 || limit > maxTargets {
		return Discovery{}, errors.New("desktop: app query must be <=256 bytes and limit 1..64")
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return Discovery{}, err
	}
	defer release()
	if err := r.ensureLocked(ctx); err != nil {
		return Discovery{}, err
	}
	reply, err := r.callLocked(ctx, "list_apps", map[string]any{})
	if err != nil {
		return Discovery{}, err
	}
	var wire struct {
		Apps []Application `json:"apps"`
	}
	if reply.IsError || json.Unmarshal(reply.Structured, &wire) != nil || wire.Apps == nil {
		return Discovery{}, errors.New("desktop: app discovery unavailable")
	}
	clear(r.apps)
	result := Discovery{Apps: []Application{}, Windows: []Window{}}
	filter := strings.ToLower(strings.TrimSpace(query))
	matchingPIDs := make(map[int]bool)
	for _, app := range wire.Apps {
		if !strings.Contains(strings.ToLower(app.Name+" "+app.BundleID), filter) {
			continue
		}
		if len(result.Apps) == limit {
			result.Truncated = true
			break
		}
		// Never accept a reference supplied by the upstream response. Only a
		// bounded, real bundle ID can become a local launch binding.
		app.Ref = ""
		if app.BundleID != "" && len(app.BundleID) <= 512 {
			app.Ref = "app-" + rand.Text()
			r.apps[app.Ref] = app.BundleID
		}
		app.Name, app.BundleID = boundedText(app.Name, 256), boundedText(app.BundleID, 512)
		result.Apps = append(result.Apps, app)
		if filter != "" && app.Running && app.PID > 0 {
			matchingPIDs[app.PID] = true
		}
	}
	windows, err := r.windowsLocked(ctx, query, limit, matchingPIDs)
	if err != nil {
		// A lost connection invalidates app references too. Keep discovered facts
		// but do not return references that the next call could no longer use.
		for i := range result.Apps {
			if _, ok := r.apps[result.Apps[i].Ref]; !ok {
				result.Apps[i].Ref = ""
			}
		}
		result.Diagnostic = "Applications were discovered, but window discovery failed; refresh before choosing a window"
		return result, nil
	}
	result.Windows = windows.Windows
	result.Truncated = result.Truncated || windows.Truncated
	return result, nil
}

func (r *Run) launchLocked(ctx context.Context, request ActRequest) (ActResult, error) {
	if request.ObservationRef != "" || request.ElementToken != "" || request.Point != nil || request.Text != "" || request.Key != "" || len(request.Keys) != 0 || request.Direction != "" || request.Amount != 0 || request.Wait != nil {
		return ActResult{}, errors.New("desktop: launch accepts only app_ref and screenshot")
	}
	bundle, ok := r.apps[request.AppRef]
	if !ok || r.manager.client == nil || !r.active {
		return ActResult{}, errors.New("desktop: stale app reference; discover the application again")
	}
	if err := ctx.Err(); err != nil {
		return ActResult{}, err
	}
	delete(r.apps, request.AppRef)
	clear(r.targets)
	r.clearObservationsLocked()
	// Launch has no public session argument in the pinned schema. It uses the
	// MCP connection's authenticated lifecycle and does not accept arbitrary
	// paths, URLs, arguments, browser-profile or inspector options from the model.
	reply, err := r.callLocked(ctx, "launch_app", map[string]any{"bundle_id": bundle})
	result := actionResult(reply, err)
	if err != nil || reply.IsError {
		return result, nil
	}
	var launched struct {
		PID      int    `json:"pid"`
		BundleID string `json:"bundle_id"`
		Windows  []struct {
			windowIdentity
			App   string `json:"app_name"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if json.Unmarshal(reply.Structured, &launched) != nil || launched.PID <= 0 || launched.BundleID != bundle {
		result.ObservationError = "Launch returned without establishing the requested app's running identity; discover it before continuing and do not repeat launch automatically"
		return result, nil
	}
	for _, window := range launched.Windows {
		if window.PID != launched.PID || window.WindowID == 0 {
			continue
		}
		if len(result.Windows) == maxTargets {
			result.WindowsTruncated = true
			break
		}
		ref := "window-" + rand.Text()
		r.targets[ref] = window.windowIdentity
		result.Windows = append(result.Windows, Window{Ref: ref, PID: window.PID, WindowID: window.WindowID, App: boundedText(window.App, 256), Title: boundedText(window.Title, 1024)})
	}
	if len(result.Windows) == 0 {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for {
			discovery, err := r.windowsLocked(waitCtx, "", maxTargets, map[int]bool{launched.PID: true})
			if err != nil {
				result.ObservationError = "App launch returned, but its window could not be checked; discover windows before continuing"
				return result, nil
			}
			result.Windows = discovery.Windows
			result.WindowsTruncated = discovery.Truncated
			if len(result.Windows) != 0 {
				break
			}
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-waitCtx.Done():
				timer.Stop()
				result.ObservationError = "App launched but no window was ready before the deadline; discover windows later without repeating launch"
				return result, nil
			case <-timer.C:
			}
		}
	}
	if len(result.Windows) == 1 {
		observation, err := r.observeLocked(ctx, ObserveRequest{TargetRef: result.Windows[0].Ref, Screenshot: request.Screenshot})
		if err != nil {
			result.ObservationError = "App window found but follow-up observation failed; rediscover and observe before acting"
			for i := range result.Windows {
				if _, ok := r.targets[result.Windows[i].Ref]; !ok {
					result.Windows[i].Ref = ""
				}
			}
		} else {
			result.Observation = &observation
		}
	} else {
		result.Diagnostic = "Multiple app windows are available; select the intended target and observe it"
	}
	return result, nil
}
