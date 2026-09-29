package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

const maxManagedApps = 4096

type managedRefusal struct {
	action, marker string
	pixels         bool
}

func (r *Run) clearManagedRefusalsLocked() {
	for target, pending := range r.managedRefusals {
		if r.manager.latest[target] == pending.marker {
			delete(r.manager.latest, target)
		}
	}
	clear(r.managedRefusals)
}

// Only locally constructed validation errors belong in this notice. Native
// transport and application permit errors remain subject to generic redaction.
func managedReject(err error) (mcpclient.Result, error) {
	result := mcpclient.Result{State: llm.ExecutionNotDispatched, IsError: true}
	managedNotice(&result, err.Error())
	return result, err
}

func managedNotice(result *mcpclient.Result, text string) {
	result.Content = append(result.Content, mcpclient.Block{Kind: mcpclient.BlockText, Text: "Managed CUA: " + text})
}

func (r *Run) managedDiscoverLocked(ctx context.Context, admission *managedAdmission, name string, args managedArguments, check func(context.Context) error) (mcpclient.Result, error) {
	wire := map[string]any{}
	if name == "list_windows" {
		if args.PID != 0 {
			wire["pid"] = args.PID
		}
		if args.OnScreenOnly != nil {
			wire["on_screen_only"] = *args.OnScreenOnly
		}
		clear(r.targets)
		r.clearObservationsLocked()
		r.clearManagedRefusalsLocked()
	} else {
		clear(r.apps)
	}
	result, err := r.managedCallLocked(ctx, admission, name, wire, check)
	if err != nil {
		return result, err
	}
	valid := false
	if name == "list_windows" {
		var state struct {
			Windows []struct{ windowIdentity } `json:"windows"`
		}
		if !result.IsError && json.Unmarshal(result.StructuredContent, &state) == nil && state.Windows != nil {
			valid = true
			for _, window := range state.Windows {
				if window.PID <= 0 || window.WindowID == 0 {
					continue
				}
				if len(r.targets) == maxTargets {
					managedNotice(&result, "only the first 64 valid windows are admitted; filter list_windows by pid for another application.")
					break
				}
				r.targets["window-"+rand.Text()] = window.windowIdentity
			}
		}
	} else {
		var state struct {
			Apps []struct {
				BundleID   string `json:"bundle_id"`
				LaunchPath string `json:"launch_path"`
			} `json:"apps"`
		}
		if !result.IsError && json.Unmarshal(result.StructuredContent, &state) == nil && state.Apps != nil {
			valid = true
			for _, app := range state.Apps {
				if app.BundleID == "" || len(app.BundleID) > 512 {
					continue
				}
				if r.manager.platform == "linux" && (app.LaunchPath == "" || len(app.LaunchPath) > 16*1024 || strings.ContainsRune(app.LaunchPath, 0)) {
					continue
				}
				if len(r.apps) == maxManagedApps {
					managedNotice(&result, "application launch admission reached its 4096-entry bound.")
					break
				}
				r.apps["app-"+rand.Text()] = appLaunchTarget{bundleID: app.BundleID, path: app.LaunchPath}
			}
		}
	}
	if !valid {
		_ = r.manager.disconnectLocked("Managed discovery returned unusable state; discover tools again")
		result.IsError = true
		managedNotice(&result, "discovery did not establish usable state; old references were retired. No automatic retry occurred.")
	}
	return result, nil
}

func (r *Run) managedTarget(args managedArguments) (string, windowIdentity, error) {
	target := windowIdentity{PID: args.PID, WindowID: args.WindowID}
	for ref, known := range r.targets {
		if known == target {
			return ref, target, nil
		}
	}
	return "", target, errors.New("desktop: window identity was not discovered in this run")
}

func (r *Run) managedObserveLocked(ctx context.Context, admission *managedAdmission, args managedArguments, check func(context.Context) error) (mcpclient.Result, error) {
	ref, target, err := r.managedTarget(args)
	if err != nil {
		return managedReject(err)
	}
	screenshot := args.Screenshot == nil || *args.Screenshot
	if screenshot && !r.options.Images {
		return managedReject(errors.New("desktop: model requires include_screenshot=false"))
	}
	pending := r.managedRefusals[target]
	continuation := pending.marker != "" && r.manager.latest[target] == pending.marker
	delete(r.managedRefusals, target)
	delete(r.manager.latest, target)
	for id, binding := range r.observations {
		if binding.target == target {
			delete(r.observations, id)
		}
	}
	result, err := r.managedCallLocked(ctx, admission, "get_window_state", map[string]any{
		"session": r.id, "pid": target.PID, "window_id": target.WindowID, "include_screenshot": screenshot,
		"include_accessibility_tree": true, "max_elements": maxElements, "max_depth": 15, "max_image_dimension": 1600, "timeout_ms": 5000, "query": args.Query,
	}, check)
	if err != nil {
		if screenshot {
			r.manager.recordCapture(false)
		}
		return result, err
	}
	observation, err := r.bindObservation(ctx, ref, target, screenshot, managedReply(result))
	if err != nil {
		_ = r.manager.disconnectLocked("Managed observation did not establish its target; discover tools again")
		result.IsError = true
		managedNotice(&result, "observation did not establish usable target state; old references were retired.")
		return result, nil
	}
	binding := r.observations[observation.Ref]
	// The generic mapper retains at most 256 blocks and 16 MiB of image views
	// plus originals. Do not admit pixels from an image it cannot retain.
	imageIndex := -1
	for i, block := range result.Content {
		if block.Kind == mcpclient.BlockImage {
			imageIndex = i
		}
	}
	imageBytes := 0
	if observation.Image != nil {
		imageBytes = len(observation.Image.Data)
		if observation.Image.Original != nil {
			imageBytes += len(observation.Image.Original.Data)
		}
	}
	if imageIndex < 0 || imageIndex >= 256 || len(result.Content[imageIndex].Data) > 16<<20 || imageBytes > 16<<20 {
		binding.capture = ""
	}
	if continuation && (!pending.pixels || binding.capture != "" && binding.snapshot != "") {
		binding.foregroundAction = pending.action
		managedNotice(&result, "a verified background refusal permits an explicit foreground continuation of the same action using this fresh observation.")
	}
	r.observations[observation.Ref] = binding
	if screenshot {
		r.manager.recordCapture(observation.Image != nil && binding.capture != "")
	}
	if observation.Diagnostic != "" {
		managedNotice(&result, observation.Diagnostic)
	}
	if observation.Truncated {
		managedNotice(&result, "semantic admission is limited to the first bounded observation entries; unadmitted tokens cannot execute.")
	}
	return result, nil
}

// This view is only for native constraint validation; the source result itself
// is returned intact to the generic mapper, including unsupported content.
func managedReply(result mcpclient.Result) Reply {
	reply := Reply{Structured: result.StructuredContent, IsError: result.IsError}
	for _, block := range result.Content {
		switch block.Kind {
		case mcpclient.BlockText:
			reply.Text = append(reply.Text, block.Text)
		case mcpclient.BlockImage:
			reply.Images = append(reply.Images, Image{Data: block.Data, MIMEType: block.MIMEType})
		}
	}
	return reply
}

func (r *Run) managedActionLocked(ctx context.Context, admission *managedAdmission, name string, args managedArguments, check func(context.Context) error) (mcpclient.Result, error) {
	_, target, err := r.managedTarget(args)
	if err != nil {
		return managedReject(err)
	}
	ref := r.manager.latest[target]
	binding, err := r.observationLocked(ref)
	if err != nil {
		return managedReject(err)
	}
	request, err := args.action(name)
	if err != nil {
		return managedReject(err)
	}
	nativeName, wire, err := r.actionArguments(binding, request)
	if err != nil {
		return managedReject(err)
	}
	if nativeName != name {
		return managedReject(errors.New("desktop: managed action name mismatch"))
	}
	delete(r.manager.latest, target)
	delete(r.observations, ref)
	delete(r.managedRefusals, target)
	result, err := r.managedCallLocked(ctx, admission, name, wire, check)
	if err == nil && r.options.Mode == ForegroundAllowed && r.manager.platform == "darwin" && request.DeliveryMode != "foreground" && safeForegroundRefusal(binding, request, managedReply(result)) {
		marker := "refusal-" + rand.Text()
		if r.managedRefusals == nil {
			r.managedRefusals = make(map[windowIdentity]managedRefusal)
		}
		r.managedRefusals[target] = managedRefusal{action: foregroundActionKey(request), marker: marker, pixels: request.Point != nil || request.Drag != nil}
		r.manager.latest[target] = marker
		managedNotice(&result, "background input was refused before dispatch by a reviewed native path. Observe the same window again before requesting foreground continuation; no input was automatically replayed.")
	} else {
		managedNotice(&result, "this action consumed its observation. Observe the window again and check the business postcondition before deciding on further input. Unknown outcomes must not be blindly repeated.")
	}
	return result, err
}

func (r *Run) managedLaunchLocked(ctx context.Context, admission *managedAdmission, args managedArguments, check func(context.Context) error) (mcpclient.Result, error) {
	var selected appLaunchTarget
	matches := 0
	for _, app := range r.apps {
		if (r.manager.platform == "linux" && app.path == args.LaunchPath) || (r.manager.platform != "linux" && app.bundleID == args.BundleID) {
			if matches > 0 && selected != app {
				return managedReject(errors.New("desktop: ambiguous discovered application"))
			}
			selected, matches = app, matches+1
		}
	}
	if matches == 0 {
		return managedReject(errors.New("desktop: application was not discovered in this run"))
	}
	clear(r.apps)
	clear(r.targets)
	r.clearObservationsLocked()
	r.clearManagedRefusalsLocked()
	wire := map[string]any{"bundle_id": selected.bundleID}
	if r.manager.platform == "linux" {
		wire = map[string]any{"launch_path": selected.path}
	}
	result, err := r.managedCallLocked(ctx, admission, "launch_app", wire, check)
	managedNotice(&result, "launch was attempted once. Discover windows and observe the intended target; do not repeat launch based only on missing window state.")
	return result, err
}
