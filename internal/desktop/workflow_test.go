package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/media"
)

// Test scenarios sequence independent managed MCP operations. These helpers are
// not a second production API: every discovery, read and mutation uses CallChecked.
type ActRequest struct {
	Kind           string         `json:"action"`
	DeliveryMode   string         `json:"delivery_mode,omitempty"`
	AppRef         string         `json:"app_ref,omitempty"`
	ObservationRef string         `json:"observation_ref"`
	ElementToken   string         `json:"element_token,omitempty"`
	Point          *Point         `json:"point,omitempty"`
	Drag           *DragGesture   `json:"drag,omitempty"`
	Text           string         `json:"text,omitempty"`
	Key            string         `json:"key,omitempty"`
	Keys           []string       `json:"keys,omitempty"`
	Direction      string         `json:"direction,omitempty"`
	Amount         int            `json:"amount,omitempty"`
	Wait           *WaitCondition `json:"wait,omitempty"`
	Screenshot     bool           `json:"screenshot"`
}

// ActResult separates dispatch/Driver response from the follow-up observation.
// An error after dispatch is data, not a Go error that a tool boundary could
// discard. A successful RPC by itself does not confirm a business postcondition.
type ActResult struct {
	Timing           ActionTiming    `json:"-"`
	Dispatched       bool            `json:"dispatched"`
	Outcome          string          `json:"outcome"`
	DriverError      bool            `json:"driver_error"`
	Driver           json.RawMessage `json:"driver,omitempty"`
	DriverText       []string        `json:"driver_text,omitempty"`
	Diagnostic       string          `json:"diagnostic,omitempty"`
	Observation      *Observation    `json:"observation,omitempty"`
	ObservationError string          `json:"observation_error,omitempty"`
	WaitState        string          `json:"wait_state,omitempty"`
	Windows          []Window        `json:"windows,omitempty"`
	WindowsTruncated bool            `json:"windows_truncated,omitempty"`
}

// ActionTiming is local diagnostic evidence, excluded from model and Session
// JSON. Driver includes the mutation RPC round trip (and transport retirement
// on failure), not just time spent inside the native input implementation.
// ConditionWait includes polling RPCs and their intervals; Observation includes
// final capture, decoding and image processing. Total also includes validation
// and gate-release cleanup. Model/Guard time lies outside this boundary.
type ActionTiming struct {
	Total, Queue, Driver, ConditionWait, Observation time.Duration
}

func actionResult(reply Reply, err error) ActResult {
	result := ActResult{Dispatched: true, Outcome: "returned", DriverError: reply.IsError}
	if len(reply.Structured) <= 64*1024 {
		result.Driver = reply.Structured
	}
	remaining := 8192
	for _, text := range reply.Text {
		if remaining == 0 {
			break
		}
		result.DriverText = append(result.DriverText, boundedText(text, remaining))
		remaining -= min(len(text), remaining)
	}
	if err != nil {
		var before beforeDispatchError
		if errors.As(err, &before) {
			result.Dispatched = false
			result.Outcome = "not_dispatched"
			result.Diagnostic = before.Error()
			return result
		}
		result.Outcome = "unknown"
		result.Diagnostic = "Action was dispatched but no complete response was received. Do not repeat it without observing and checking the target."
		return result
	}
	if len(reply.Structured) > 64*1024 {
		result.Diagnostic = "Driver action details exceeded the result limit; inspect the fresh observation before continuing"
	}
	if reply.IsError && result.Diagnostic == "" {
		result.Diagnostic = "Driver reported an action error; partial effects may have occurred"
	}
	return result
}

func (r *Run) fixtureCall(ctx context.Context, name string, args map[string]any) (mcpclient.Result, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	return r.CallChecked(ctx, name, raw, nil)
}

func (r *Run) discoverWindows(ctx context.Context, query string, limit int) (Discovery, error) {
	return r.fixtureWindows(ctx, query, limit, nil)
}

func (r *Run) fixtureWindows(ctx context.Context, query string, limit int, pids map[int]bool) (Discovery, error) {
	if len(query) > 256 || limit < 1 || limit > maxTargets {
		return Discovery{}, errors.New("invalid fixture discovery")
	}
	if _, err := r.Tools(ctx); err != nil {
		return Discovery{}, err
	}
	args := map[string]any{}
	if len(pids) == 1 {
		for pid := range pids {
			args["pid"] = pid
		}
	}
	raw, err := r.fixtureCall(ctx, "list_windows", args)
	if err != nil {
		return Discovery{}, err
	}
	if raw.IsError {
		return Discovery{}, errors.New("window discovery unavailable")
	}
	var state struct {
		Windows []struct {
			windowIdentity
			App   string `json:"app_name"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(raw.StructuredContent, &state); err != nil {
		return Discovery{}, err
	}
	_, release, err := r.acquire(ctx)
	if err != nil {
		return Discovery{}, err
	}
	defer release()
	result := Discovery{Windows: []Window{}}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, window := range state.Windows {
		matches := (query == "" && len(pids) == 0) || pids[window.PID] || (query != "" && strings.Contains(strings.ToLower(window.App+" "+window.Title), query))
		if !matches {
			continue
		}
		ref := ""
		for key, target := range r.targets {
			if target == window.windowIdentity {
				ref = key
				break
			}
		}
		if ref == "" {
			continue
		}
		if len(result.Windows) == limit {
			result.Truncated = true
			break
		}
		result.Windows = append(result.Windows, Window{Ref: ref, PID: window.PID, WindowID: window.WindowID, App: window.App, Title: window.Title})
	}
	return result, nil
}

func (r *Run) discoverApps(ctx context.Context, query string, limit int) (Discovery, error) {
	if len(query) > 256 || limit < 1 || limit > maxTargets {
		return Discovery{}, errors.New("invalid fixture app discovery")
	}
	if _, err := r.Tools(ctx); err != nil {
		return Discovery{}, err
	}
	raw, err := r.fixtureCall(ctx, "list_apps", map[string]any{})
	if err != nil {
		return Discovery{}, err
	}
	if raw.IsError {
		return Discovery{}, errors.New("app discovery unavailable")
	}
	var state struct {
		Apps []struct {
			Application
			LaunchPath string `json:"launch_path"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw.StructuredContent, &state); err != nil {
		return Discovery{}, err
	}
	_, release, err := r.acquire(ctx)
	if err != nil {
		return Discovery{}, err
	}
	result := Discovery{Apps: []Application{}, Windows: []Window{}}
	filter := strings.ToLower(strings.TrimSpace(query))
	pids := map[int]bool{}
	for _, native := range state.Apps {
		app := native.Application
		if !strings.Contains(strings.ToLower(app.Name+" "+app.BundleID), filter) {
			continue
		}
		if len(result.Apps) == limit {
			result.Truncated = true
			break
		}
		app.Ref = ""
		for ref, known := range r.apps {
			if known.bundleID == app.BundleID && (r.manager.platform != "linux" || known.path == native.LaunchPath) {
				app.Ref = ref
				break
			}
		}
		result.Apps = append(result.Apps, app)
		if filter != "" && app.Running && app.PID > 0 {
			pids[app.PID] = true
		}
	}
	release()
	windows, err := r.fixtureWindows(ctx, query, limit, pids)
	if err != nil {
		result.Diagnostic = "Applications discovered, window discovery unavailable"
		return result, nil
	}
	result.Windows = windows.Windows
	result.Truncated = result.Truncated || windows.Truncated
	return result, nil
}

func (r *Run) observeWindow(ctx context.Context, request ObserveRequest) (Observation, error) {
	_, release, err := r.acquire(ctx)
	if err != nil {
		return Observation{}, err
	}
	target, ok := r.targets[request.TargetRef]
	release()
	if !ok {
		return Observation{}, errors.New("stale target")
	}
	raw, err := r.fixtureCall(ctx, "get_window_state", map[string]any{"pid": target.PID, "window_id": target.WindowID, "include_screenshot": request.Screenshot, "query": request.Query})
	if err != nil {
		return Observation{}, err
	}
	_, release, err = r.acquire(ctx)
	if err != nil {
		return Observation{}, err
	}
	defer release()
	ref := r.manager.latest[target]
	binding, ok := r.observations[ref]
	if !ok {
		return Observation{}, errors.New("observation unavailable")
	}
	var state struct {
		Elements []Element `json:"elements"`
		Complete bool      `json:"elements_complete"`
		Degraded bool      `json:"degraded"`
		Reason   string    `json:"degraded_reason"`
	}
	if err := json.Unmarshal(raw.StructuredContent, &state); err != nil {
		return Observation{}, err
	}
	result := Observation{Ref: ref, TargetRef: request.TargetRef, Complete: state.Complete && !raw.IsError && r.manager.platform != "linux", Degraded: state.Degraded || raw.IsError, Diagnostic: state.Reason, Elements: []Element{}}
	if binding.foregroundAction != "" {
		var action ActRequest
		_ = json.Unmarshal([]byte(binding.foregroundAction), &action)
		result.ForegroundAction = action.Kind
	}
	textBytes := 0
	for _, element := range state.Elements {
		textBytes += len(element.Token) + len(element.Role) + len(element.Label) + len(element.Value)
		if len(result.Elements) == maxElements || textBytes > maxObservationText {
			result.Truncated = true
			result.Complete = false
			break
		}
		if _, ok := binding.tokens[element.Token]; !ok {
			element.Token = ""
		}
		result.Elements = append(result.Elements, element)
	}
	if request.Screenshot {
		for _, block := range raw.Content {
			if block.Kind == mcpclient.BlockImage {
				prepared, err := media.Prepare(ctx, llm.ImageContent{Data: block.Data, MIMEType: block.MIMEType}, nil)
				if err == nil {
					result.Image = &prepared
					result.ImageWidth = prepared.Width
					result.ImageHeight = prepared.Height
				}
			}
		}
		if result.Image == nil || binding.capture == "" {
			result.Degraded = true
			result.Diagnostic = "Screenshot unavailable or unbound"
		}
	}
	return result, nil
}

func (r *Run) actAndObserve(ctx context.Context, request ActRequest) (result ActResult, returnErr error) {
	started := time.Now()
	var timing ActionTiming
	defer func() { timing.Total = time.Since(started); result.Timing = timing }()
	queued := time.Now()
	_, release, err := r.acquire(ctx)
	timing.Queue = time.Since(queued)
	if err != nil {
		return result, err
	}
	if request.Screenshot && !r.options.Images {
		release()
		return result, errors.New("images unavailable")
	}
	if request.Kind == "launch" {
		release()
		return r.fixtureLaunch(ctx, request, &timing)
	}
	binding, err := r.observationLocked(request.ObservationRef)
	if err != nil {
		release()
		return result, err
	}
	if request.AppRef != "" {
		release()
		return result, errors.New("unrelated app ref")
	}
	release()
	if request.Kind == "wait" {
		return r.fixtureWait(ctx, binding, request, &timing)
	}
	args := map[string]any{"pid": binding.target.PID, "window_id": binding.target.WindowID}
	name := request.Kind
	if request.ElementToken != "" {
		args["element_token"] = request.ElementToken
	}
	if request.Point != nil {
		args["x"], args["y"] = request.Point.X, request.Point.Y
	}
	if request.DeliveryMode != "" {
		args["delivery_mode"] = request.DeliveryMode
	}
	switch name {
	case "double_click":
		name = "click"
		args["count"] = 2
	case "right_click":
		name = "click"
		args["button"] = "right"
	case "key":
		name = "press_key"
	}
	if request.Text != "" {
		args["text"] = request.Text
	}
	if request.Kind == "set_value" {
		delete(args, "text")
		args["value"] = request.Text
	}
	if request.Key != "" {
		args["key"] = request.Key
	}
	if len(request.Keys) > 0 {
		args["keys"] = request.Keys
	}
	if request.Direction != "" {
		args["direction"] = request.Direction
	}
	if request.Amount != 0 {
		args["amount"] = request.Amount
	}
	if request.Wait != nil {
		args["wait"] = request.Wait
	}
	if request.Drag != nil {
		if request.Drag.From != nil {
			args["from_x"] = request.Drag.From.X
			args["from_y"] = request.Drag.From.Y
		}
		if request.Drag.To != nil {
			args["to_x"] = request.Drag.To.X
			args["to_y"] = request.Drag.To.Y
		}
		if request.Drag.DurationMS != 0 {
			args["duration_ms"] = request.Drag.DurationMS
		}
	}
	phase := time.Now()
	raw, err := r.fixtureCall(ctx, name, args)
	timing.Driver = time.Since(phase)
	result = actionResult(managedReply(raw), err)
	if raw.State == llm.ExecutionNotDispatched {
		result.Dispatched = false
		result.Outcome = "not_dispatched"
		// A native pre-dispatch capability failure is a returned execution fact;
		// local validation failures remain fixture errors.
		var before beforeDispatchError
		if errors.As(err, &before) {
			return result, nil
		}
		return result, err
	}
	if err != nil {
		return result, nil
	}
	screenshot := request.Screenshot
	if raw.IsError && r.options.Mode == ForegroundAllowed && (request.Point != nil || request.Drag != nil) {
		screenshot = true
	}
	phase = time.Now()
	after, err := r.observeWindow(ctx, ObserveRequest{TargetRef: binding.targetRef, Screenshot: screenshot})
	timing.Observation = time.Since(phase)
	if err != nil {
		result.ObservationError = "Action returned; explicit follow-up observation unavailable"
		return result, nil
	}
	result.Observation = &after
	return result, nil
}

func (r *Run) fixtureLaunch(ctx context.Context, request ActRequest, timing *ActionTiming) (ActResult, error) {
	if request.Drag != nil || request.DeliveryMode != "" || request.ObservationRef != "" || request.ElementToken != "" || request.Point != nil || request.Text != "" || request.Key != "" || len(request.Keys) > 0 || request.Direction != "" || request.Amount != 0 || request.Wait != nil {
		return ActResult{}, errors.New("unrelated launch fields")
	}
	_, release, err := r.acquire(ctx)
	if err != nil {
		return ActResult{}, err
	}
	app, ok := r.apps[request.AppRef]
	release()
	if !ok {
		return ActResult{}, errors.New("stale app")
	}
	args := map[string]any{"bundle_id": app.bundleID}
	if r.manager.platform == "linux" {
		args = map[string]any{"launch_path": app.path}
	}
	phase := time.Now()
	raw, err := r.fixtureCall(ctx, "launch_app", args)
	timing.Driver = time.Since(phase)
	result := actionResult(managedReply(raw), err)
	if raw.State == llm.ExecutionNotDispatched {
		return result, err
	}
	if err != nil || raw.IsError {
		return result, nil
	}
	var launched struct {
		PID      int    `json:"pid"`
		BundleID string `json:"bundle_id"`
		Name     string `json:"name"`
		Running  bool   `json:"running"`
	}
	decodeErr := json.Unmarshal(raw.StructuredContent, &launched)
	matched := launched.BundleID == app.bundleID
	if r.manager.platform == "linux" {
		matched = launched.Name == app.path && launched.Running
	}
	if decodeErr != nil || launched.PID <= 0 || !matched {
		result.ObservationError = "Launch did not establish expected process; rediscover without repeating it"
		return result, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		phase = time.Now()
		discovery, err := r.fixtureWindows(waitCtx, "", maxTargets, map[int]bool{launched.PID: true})
		timing.ConditionWait += time.Since(phase)
		if err != nil {
			result.ObservationError = "Explicit window discovery unavailable"
			return result, nil
		}
		result.Windows, result.WindowsTruncated = discovery.Windows, discovery.Truncated
		if len(result.Windows) > 0 {
			break
		}
		phase = time.Now()
		err = waitNativeProbe(waitCtx)
		timing.ConditionWait += time.Since(phase)
		if err != nil {
			result.ObservationError = "Fixture window deadline reached"
			return result, nil
		}
	}
	if len(result.Windows) != 1 {
		result.Diagnostic = "Select among multiple windows"
		return result, nil
	}
	phase = time.Now()
	observation, err := r.observeWindow(ctx, ObserveRequest{TargetRef: result.Windows[0].Ref, Screenshot: request.Screenshot})
	timing.Observation = time.Since(phase)
	if err != nil {
		result.ObservationError = "Explicit launch observation unavailable"
	} else {
		result.Observation = &observation
	}
	return result, nil
}

func (r *Run) fixtureWait(ctx context.Context, binding observationBinding, request ActRequest, timing *ActionTiming) (ActResult, error) {
	condition := request.Wait
	if condition == nil || strings.TrimSpace(condition.Text) == "" || len(condition.Text) > 256 || condition.TimeoutMS < 1 || condition.TimeoutMS > 10000 {
		return ActResult{}, errors.New("desktop: wait requires semantic text of 1..256 bytes and timeout_ms of 1..10000")
	}
	if request.Drag != nil || request.DeliveryMode != "" || request.Point != nil || request.ElementToken != "" || request.Text != "" || request.Key != "" || len(request.Keys) != 0 || request.Direction != "" || request.Amount != 0 {
		return ActResult{}, errors.New("desktop: wait contains unrelated action fields")
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(condition.TimeoutMS)*time.Millisecond)
	defer cancel()
	result := ActResult{Outcome: "returned", WaitState: "unknown"}
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if waitCtx.Err() != nil {
			break
		}
		phase := time.Now()
		observation, err := r.observeWindow(waitCtx, ObserveRequest{TargetRef: binding.targetRef, Query: condition.Text})
		timing.ConditionWait += time.Since(phase)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if r.ctx.Err() != nil {
				return result, r.ctx.Err()
			}
			result.Observation = nil // a failed refresh invalidates older refs too
			result.WaitState = "unknown"
			result.ObservationError = "Condition could not be checked; observe the target before continuing"
			return result, nil
		}
		result.Observation = &observation
		result.WaitState = semanticCondition(observation, condition.Text)
		if result.WaitState == "satisfied" {
			break
		}
		phase = time.Now()
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
		timing.ConditionWait += time.Since(phase)
	}
	if request.Screenshot {
		if waitCtx.Err() != nil {
			result.Diagnostic = "Wait deadline reached; final state is semantic only"
			return result, nil
		}
		// Capture once at completion, then re-evaluate using that exact final
		// observation. No screenshot is taken on every polling tick.
		phase := time.Now()
		observation, err := r.observeWindow(waitCtx, ObserveRequest{TargetRef: binding.targetRef, Screenshot: true, Query: condition.Text})
		timing.Observation = time.Since(phase)
		if err != nil {
			result.Observation = nil // the attempted refresh invalidated its refs
			result.WaitState = "unknown"
			result.ObservationError = "Final observation unavailable; the earlier condition may have changed"
			return result, nil
		}
		result.Observation = &observation
		result.WaitState = semanticCondition(observation, condition.Text)
	}
	return result, nil
}

func semanticCondition(observation Observation, text string) string {
	for _, element := range observation.Elements {
		if strings.Contains(element.Label, text) || strings.Contains(element.Value, text) {
			return "satisfied"
		}
	}
	if observation.Complete && !observation.Degraded && !observation.Truncated {
		return "unsatisfied"
	}
	return "unknown"
}

// Older native fixtures implement the small lifecycle transport. Give their
// fake the same managed contract; no production execution falls back to call.
func (f *fakeDriver) Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	var inventory schemaInventory
	if err := json.Unmarshal(macSchemaInventory, &inventory); err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	// The fixtures cover both admitted platforms; their launch property union is
	// projected back to the platform's exact managed field set by Run.Tools.
	var launch map[string]json.RawMessage
	_ = json.Unmarshal(inventory.Tools["launch_app"], &launch)
	var properties map[string]json.RawMessage
	_ = json.Unmarshal(launch["properties"], &properties)
	properties["launch_path"] = json.RawMessage(`{"type":"string"}`)
	launch["properties"], _ = json.Marshal(properties)
	launch["required"] = json.RawMessage(`[]`)
	inventory.Tools["launch_app"], _ = json.Marshal(launch)
	result := mcpclient.Catalog[mcpclient.Tool]{Generation: 1, Complete: true}
	for name, schema := range inventory.Tools {
		result.Items = append(result.Items, mcpclient.Tool{Name: name, InputSchema: schema})
	}
	return result, nil
}
func (f *fakeDriver) ToolGeneration() uint64 { return 1 }
func (f *fakeDriver) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if check != nil {
		if err := check(ctx); err != nil {
			return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
		}
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	// Preserve native scalar types expected by pre-existing fixture handlers.
	for _, key := range []string{"pid", "max_elements", "max_depth", "timeout_ms", "max_image_dimension", "amount", "duration_ms", "count"} {
		if value, ok := args[key].(float64); ok {
			args[key] = int(value)
		}
	}
	if value, ok := args["window_id"].(float64); ok {
		args["window_id"] = uint64(value)
	}
	reply, err := f.call(ctx, name, args)
	result := mcpclient.Result{StructuredContent: reply.Structured, IsError: reply.IsError, State: llm.ExecutionReturned}
	if err != nil {
		result.State = llm.ExecutionUnknown
		var before beforeDispatchError
		if errors.As(err, &before) {
			result.State = llm.ExecutionNotDispatched
		}
	}
	for _, text := range reply.Text {
		result.Content = append(result.Content, mcpclient.Block{Kind: mcpclient.BlockText, Text: text})
	}
	for _, image := range reply.Images {
		result.Content = append(result.Content, mcpclient.Block{Kind: mcpclient.BlockImage, Data: image.Data, MIMEType: image.MIMEType})
	}
	return result, err
}

// WaitCondition asks for visible semantic text, not a generic visual-stability
// heuristic. A missing match in an incomplete projection remains unknown.
type WaitCondition struct {
	Text      string `json:"text"`
	TimeoutMS int    `json:"timeout_ms"`
}
