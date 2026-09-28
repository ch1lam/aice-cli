package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// The Run consumes the admitted transport, never an arbitrary MCP connection.
// Native lifecycle, target state and serialization remain Manager-owned.
type managedClient interface {
	Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error)
	ToolGeneration() uint64
	CallChecked(context.Context, string, json.RawMessage, func(context.Context) error) (mcpclient.Result, error)
}

type managedAdmission struct {
	client                                     managedClient
	epoch, managerGeneration, clientGeneration uint64
}

// ToolGeneration is local and safe inside a final application dispatch check.
// It must not acquire the Manager gate already held by CallChecked.
func (r *Run) ToolGeneration() uint64 {
	admission := r.managed.Load()
	if admission == nil {
		return 0
	}
	status := r.manager.Status()
	if r.closed.Load() || r.ctx.Err() != nil || r.manager.ctx.Err() != nil || !status.Connected || status.Generation != admission.managerGeneration || admission.client.ToolGeneration() != admission.clientGeneration {
		return admission.epoch + 1
	}
	return admission.epoch
}

// Tools explicitly admits a connection and run-owned native session. It does
// not enumerate apps/windows, capture, or perform input. Internal lifecycle and
// OS setup methods are excluded from the model-facing managed catalog.
func (r *Run) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	defer release()
	if err := r.ensureLocked(ctx); err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	client, ok := r.manager.client.(managedClient)
	if !ok {
		return mcpclient.Catalog[mcpclient.Tool]{}, errors.New("desktop: admitted MCP capabilities unavailable")
	}
	catalog, err := client.Tools(ctx)
	if err == nil && !catalog.Complete {
		err = errors.New("desktop: admitted catalog is incomplete")
	}
	if err != nil {
		_ = r.manager.disconnectLocked("Driver catalog unavailable; discover again to re-admit")
		return catalog, err
	}
	items := make([]mcpclient.Tool, 0, len(catalog.Items))
	for _, descriptor := range catalog.Items {
		fields := managedFields(descriptor.Name, r.manager.platform)
		if fields == nil {
			continue
		}
		schema, err := managedSchema(descriptor.Name, descriptor.InputSchema, fields)
		if err != nil {
			return mcpclient.Catalog[mcpclient.Tool]{}, err
		}
		descriptor.InputSchema = schema
		descriptor.Description += " Managed CUA: session is run-owned. Use only discovered app/window identities and the latest observed element tokens or displayed-image pixels. Each mutation consumes the observation; call get_window_state again before another action. No file output, arbitrary launch options or alternative targets."
		items = append(items, descriptor)
	}
	if len(items) != 11 {
		return mcpclient.Catalog[mcpclient.Tool]{}, errors.New("desktop: managed tool inventory incomplete")
	}
	generation := r.manager.Status().Generation
	previous := r.managed.Load()
	if previous == nil || previous.managerGeneration != generation || previous.clientGeneration != catalog.Generation {
		epoch := uint64(2)
		if previous != nil {
			epoch = previous.epoch + 2
		}
		r.managed.Store(&managedAdmission{client: client, epoch: epoch, managerGeneration: generation, clientGeneration: catalog.Generation})
	}
	catalog.Items, catalog.Generation = items, r.managed.Load().epoch
	if err := ctx.Err(); err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	if r.ToolGeneration() != catalog.Generation {
		_ = r.manager.disconnectLocked("Driver catalog changed during managed discovery")
		return mcpclient.Catalog[mcpclient.Tool]{}, errDriverCatalogChanged
	}
	return catalog, nil
}

// CallChecked is a single model operation. Discovery must have admitted its
// connection first; execution never reconnects or retries a native action.
func (r *Run) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	notDispatched := mcpclient.Result{State: llm.ExecutionNotDispatched}
	args, err := decodeManagedArguments(name, r.manager.platform, raw)
	if err != nil {
		return managedReject(err)
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return notDispatched, err
	}
	defer release()
	admission := r.managed.Load()
	if admission == nil || r.ToolGeneration() != admission.epoch || !r.active {
		return managedReject(errors.New("desktop: managed admission expired; discover tools again"))
	}
	// Reject the current permit before changing even local observation state.
	if check != nil {
		if err := check(ctx); err != nil {
			return notDispatched, err
		}
	}
	switch name {
	case "list_apps", "list_windows":
		return r.managedDiscoverLocked(ctx, admission, name, args, check)
	case "get_window_state":
		return r.managedObserveLocked(ctx, admission, args, check)
	case "launch_app":
		return r.managedLaunchLocked(ctx, admission, args, check)
	default:
		return r.managedActionLocked(ctx, admission, name, args, check)
	}
}

func (r *Run) managedCallLocked(ctx context.Context, admission *managedAdmission, name string, args map[string]any, check func(context.Context) error) (mcpclient.Result, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, errors.New("desktop: invalid managed arguments")
	}
	result, err := admission.client.CallChecked(ctx, name, raw, func(ctx context.Context) error {
		if r.ToolGeneration() != admission.epoch {
			return errDriverCatalogChanged
		}
		if check != nil {
			return check(ctx)
		}
		return nil
	})
	if err != nil && (result.State != llm.ExecutionNotDispatched || errors.Is(err, errDriverCatalogChanged) || errors.Is(err, mcpclient.ErrClosed)) {
		_ = r.manager.disconnectLocked("Managed Driver call failed; discover again to re-admit without replaying input")
	}
	return result, err
}

func managedFields(name, platform string) []string {
	switch name {
	case "list_apps":
		return []string{}
	case "list_windows":
		return []string{"pid", "on_screen_only"}
	case "get_window_state":
		return []string{"pid", "window_id", "include_screenshot", "query"}
	case "launch_app":
		if platform == "linux" {
			return []string{"launch_path"}
		}
		return []string{"bundle_id"}
	case "click":
		return []string{"pid", "window_id", "element_token", "x", "y", "button", "count", "delivery_mode"}
	case "drag":
		return []string{"pid", "window_id", "from_x", "from_y", "to_x", "to_y", "duration_ms", "delivery_mode"}
	case "type_text":
		return []string{"pid", "window_id", "element_token", "x", "y", "text", "delivery_mode"}
	case "set_value":
		return []string{"pid", "window_id", "element_token", "value"}
	case "press_key":
		return []string{"pid", "window_id", "element_token", "key", "delivery_mode"}
	case "hotkey":
		return []string{"pid", "window_id", "element_token", "keys", "delivery_mode"}
	case "scroll":
		return []string{"pid", "window_id", "element_token", "x", "y", "direction", "amount", "delivery_mode"}
	default:
		return nil
	}
}

// All supported pinned schemas are flat object schemas. Restrict only their
// property set and required fields, retaining each admitted property verbatim.
// Refuse an unfamiliar shape instead of weakening a new upstream constraint.
func managedSchema(name string, raw json.RawMessage, fields []string) (json.RawMessage, error) {
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil {
		return nil, errors.New("desktop: invalid managed schema")
	}
	for key := range schema {
		if !slices.Contains([]string{"type", "properties", "required", "additionalProperties"}, key) {
			return nil, errors.New("desktop: unmanaged schema constraint")
		}
	}
	var properties map[string]json.RawMessage
	var required []string
	if json.Unmarshal(schema["properties"], &properties) != nil {
		return nil, errors.New("desktop: invalid managed properties")
	}
	if len(schema["required"]) > 0 && json.Unmarshal(schema["required"], &required) != nil {
		return nil, errors.New("desktop: invalid managed requirements")
	}
	for _, key := range fields {
		if _, ok := properties[key]; !ok {
			return nil, errors.New("desktop: missing managed property")
		}
	}
	for key := range properties {
		if !slices.Contains(fields, key) {
			delete(properties, key)
		}
	}
	for _, key := range required {
		if !slices.Contains(fields, key) {
			return nil, errors.New("desktop: managed projection removed a required property")
		}
	}
	for _, key := range []string{"pid", "window_id", "bundle_id", "launch_path"} {
		if name != "list_windows" && slices.Contains(fields, key) && !slices.Contains(required, key) {
			required = append(required, key)
		}
	}
	schema["properties"], _ = json.Marshal(properties)
	if required == nil {
		required = []string{}
	}
	schema["required"], _ = json.Marshal(required)
	schema["additionalProperties"] = json.RawMessage(`false`)
	return json.Marshal(schema)
}
