package desktop

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// The Run consumes the admitted transport, never an arbitrary MCP connection.
// Native lifecycle and serialization remain Manager-owned.
type managedClient interface {
	Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error)
	ToolGeneration() uint64
	CallChecked(context.Context, string, json.RawMessage, func(context.Context) error) (mcpclient.Result, error)
}

type managedAdmission struct {
	client                                     managedClient
	tools                                      map[string]managedTool
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
	admitted := make(map[string]managedTool)
	for _, descriptor := range catalog.Items {
		if !managedToolName(descriptor.Name) {
			continue
		}
		schema, tool, err := managedSchema(descriptor.InputSchema)
		if err != nil {
			return mcpclient.Catalog[mcpclient.Tool]{}, err
		}
		descriptor.InputSchema = schema
		descriptor.Description += " AICE host policy: session is supplied by the host. Other parameters and results follow the native Driver contract, including snapshot and capture validity. Use Driver source screenshot coordinates; generic image views may be resized. Foreground delivery and desktop input require the configured foreground_allowed mode."
		if !r.options.Images && descriptor.Name == "get_window_state" {
			descriptor.Description += " This model cannot receive images: set include_screenshot=false."
		}
		items = append(items, descriptor)
		admitted[descriptor.Name] = tool
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
		r.managed.Store(&managedAdmission{client: client, tools: admitted, epoch: epoch, managerGeneration: generation, clientGeneration: catalog.Generation})
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
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return notDispatched, err
	}
	defer release()
	admission := r.managed.Load()
	if admission == nil || r.ToolGeneration() != admission.epoch || !r.active {
		return managedReject(errors.New("desktop: managed admission expired; discover tools again"))
	}
	tool, ok := admission.tools[name]
	if !ok {
		return managedReject(errors.New("desktop: tool is outside the model-facing catalog"))
	}
	args, err := r.managedArguments(tool, name, raw)
	if err != nil {
		return managedReject(err)
	}
	// The application permit is checked here and again immediately before the
	// generic MCP transport writes. No native input is automatically replayed.
	if check != nil {
		if err := check(ctx); err != nil {
			return notDispatched, err
		}
	}
	wire, err := json.Marshal(args)
	if err != nil {
		return managedReject(errors.New("desktop: invalid managed arguments"))
	}
	result, err := admission.client.CallChecked(ctx, name, wire, func(ctx context.Context) error {
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
	} else if err == nil && managedSessionEnded(name, result) {
		// Preserve the Driver result exactly. This is connection retirement, not
		// a retry, input recovery, or a second target/observation state machine.
		_ = r.manager.disconnectLocked("Driver session expired; discover tools again to establish a fresh connection without replaying input")
	}
	return result, err
}

// ServerInfo reads only an already admitted connection. Unlike Tools, it never
// connects, starts a session, captures, or requests native authorization.
func (r *Run) ServerInfo(ctx context.Context) (mcpclient.Info, error) {
	_, release, err := r.acquire(ctx)
	if err != nil {
		return mcpclient.Info{}, err
	}
	defer release()
	admission := r.managed.Load()
	if admission == nil || r.ToolGeneration() != admission.epoch || !r.active {
		return mcpclient.Info{}, errors.New("desktop: discover tools before requesting server information")
	}
	provider, ok := admission.client.(interface{ Info() mcpclient.Info })
	if !ok {
		return mcpclient.Info{}, mcpclient.ErrUnsupported
	}
	return provider.Info(), nil
}

func managedToolName(name string) bool {
	switch name {
	case "list_apps", "list_windows", "get_window_state", "launch_app", "click", "drag", "type_text", "set_value", "press_key", "hotkey", "scroll":
		return true
	default:
		return false
	}
}
