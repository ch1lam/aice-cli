// Package desktop owns Cua connections and desktop operation lifetimes.
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

const (
	DriverVersion   = "0.29.1"
	ProtocolVersion = "2025-06-18"
	maxMessageBytes = 24 * 1024 * 1024
	connectTimeout  = 15 * time.Second
	callTimeout     = 30 * time.Second
)

// Reply is the native validation view used by setup, lifecycle and observation
// checks, including managed MCP calls. The generic client retains the ordered
// source result separately; this view is not the model-facing transcript.
type Reply struct {
	Structured json.RawMessage
	Text       []string
	Images     []Image
	IsError    bool
}

type Image struct {
	Data     []byte
	MIMEType string
}

// beforeDispatchError is reserved for failures established before dispatch.
// Native callers treat other transport failures as potentially dispatched.
type beforeDispatchError struct{ error }

func (e beforeDispatchError) Unwrap() error { return e.error }

// client admits only the pinned Driver identity and reviewed platform tools.
// Protocol, framing, cancellation and child ownership belong to mcpclient.
type client struct {
	connection *mcpclient.Client
	tools      map[string]json.RawMessage
	catalog    mcpclient.Catalog[mcpclient.Tool]
	closed     atomic.Bool
}

func connect(ctx context.Context, config mcpclient.Config) (*client, error) {
	return connectReviewed(ctx, config, reviewedMacTools)
}

func connectReviewed(ctx context.Context, config mcpclient.Config, review func(map[string]json.RawMessage) (map[string]json.RawMessage, error)) (*client, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	config.ProtocolVersion = ProtocolVersion
	config.ConnectTimeout, config.CallTimeout = connectTimeout, callTimeout
	config.Limits = mcpclient.Limits{MessageBytes: maxMessageBytes, CatalogItems: 256, Pages: 16}
	connection, err := mcpclient.Open(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("desktop: initialize: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = connection.Close()
		}
	}()
	info := connection.Info()
	if info.Name != "cua-driver" || info.Version != DriverVersion {
		return nil, errors.New("desktop: incompatible Driver identity or protocol")
	}
	catalog, err := connection.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("desktop: discover tools: %w", err)
	}
	if !catalog.Complete {
		return nil, errors.New("desktop: incomplete Driver tool discovery")
	}
	schemas := make(map[string]json.RawMessage, len(catalog.Items))
	for _, tool := range catalog.Items {
		schemas[tool.Name] = tool.InputSchema
	}
	schemas, err = review(schemas)
	if err != nil {
		return nil, err
	}
	// Keep complete descriptors for generic mapping, but only for admitted names.
	admitted := make([]mcpclient.Tool, 0, len(schemas))
	for _, descriptor := range catalog.Items {
		if _, ok := schemas[descriptor.Name]; ok {
			admitted = append(admitted, descriptor)
		}
	}
	catalog.Items = admitted
	ok = true
	return &client{connection: connection, tools: schemas, catalog: catalog}, nil
}

// call sends exactly once. Neither domain errors nor EOF/cancellation are retried.
func (c *client) call(ctx context.Context, name string, arguments any) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, beforeDispatchError{err}
	}
	if _, ok := c.tools[name]; !ok {
		return Reply{}, beforeDispatchError{fmt.Errorf("desktop: capability %s unavailable", name)}
	}
	// The SDK previously normalized absent arguments to an empty object.
	raw := json.RawMessage(`{}`)
	if arguments != nil {
		var err error
		raw, err = json.Marshal(arguments)
		if err != nil {
			return Reply{}, beforeDispatchError{errors.New("desktop: invalid tool arguments")}
		}
	}
	result, err := c.CallChecked(ctx, name, raw, nil)
	if err != nil {
		if errors.Is(err, mcpclient.ErrConfig) || errors.Is(err, errDriverCatalogChanged) || errors.Is(err, mcpclient.ErrClosed) {
			return Reply{}, beforeDispatchError{err}
		}
		return Reply{}, fmt.Errorf("desktop: %s transport failed; dispatched outcome may be unknown: %w", name, err)
	}
	reply := Reply{IsError: result.IsError, Structured: result.StructuredContent}
	for _, part := range result.Content {
		switch part.Kind {
		case mcpclient.BlockText:
			reply.Text = append(reply.Text, part.Text)
		case mcpclient.BlockImage:
			reply.Images = append(reply.Images, Image{Data: part.Data, MIMEType: part.MIMEType})
		default:
			return reply, errors.New("desktop: unsupported Driver content; action outcome must be checked before continuing")
		}
	}
	return reply, nil
}

// A list_changed notification invalidates admission; it never expands the
// reviewed set or silently replaces an executable schema. Re-admit a new client.
var errDriverCatalogChanged = errors.New("desktop: Driver catalog changed; reconnect and review the current schema before calling tools")

func (c *client) ToolGeneration() uint64 { return c.connection.ToolGeneration() }

// Tools returns the admitted catalog without native I/O. Each caller owns its
// descriptors and exact JSON fields. Native lifecycle/target authorization is
// still the Run owner's responsibility; this is not a model execution grant.
func (c *client) Tools(ctx context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	if err := ctx.Err(); err != nil {
		return mcpclient.Catalog[mcpclient.Tool]{}, err
	}
	if c.closed.Load() {
		return mcpclient.Catalog[mcpclient.Tool]{}, mcpclient.ErrClosed
	}
	if c.ToolGeneration() != c.catalog.Generation {
		return mcpclient.Catalog[mcpclient.Tool]{}, errDriverCatalogChanged
	}
	catalog := c.catalog
	catalog.Items = slices.Clone(c.catalog.Items)
	for i := range catalog.Items {
		catalog.Items[i].InputSchema = slices.Clone(catalog.Items[i].InputSchema)
		catalog.Items[i].OutputSchema = slices.Clone(catalog.Items[i].OutputSchema)
		catalog.Items[i].Annotations = slices.Clone(catalog.Items[i].Annotations)
	}
	return catalog, nil
}

// CallChecked retains the generic client's ordered/raw result and dispatch
// state. Internal lifecycle/setup calls and native acceptance helpers also
// consume this boundary through call; managed model operations use it directly.
// Recheck admission after the generic connection queue and before its write.
func (c *client) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	checkAdmission := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.closed.Load() {
			return mcpclient.ErrClosed
		}
		if _, ok := c.tools[name]; !ok {
			return errors.New("desktop: unreviewed Driver tool")
		}
		if c.ToolGeneration() != c.catalog.Generation {
			return errDriverCatalogChanged
		}
		return nil
	}
	if err := checkAdmission(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	return c.connection.CallChecked(ctx, name, args, func(ctx context.Context) error {
		if err := checkAdmission(ctx); err != nil {
			return err
		}
		if check != nil {
			return check(ctx)
		}
		return nil
	})
}

func (c *client) close() error {
	c.closed.Store(true)
	return c.connection.Close()
}

// driverEnvironment deliberately excludes provider credentials, loader injection
// and inherited Cua authority overrides. Shared service preferences are untouched.
func driverEnvironment(environ []string) []string {
	var result []string
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USER", "LOGNAME", "PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY", "SYSTEMROOT", "WINDIR", "USERPROFILE", "LOCALAPPDATA", "APPDATA":
			result = append(result, entry)
		}
	}
	return append(result, "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "CUA_DRIVER_RS_UPDATE_CHECK=false", "CUA_DRIVER_PERMISSION_MODE=standard")
}

func driverMCPConfig(binary, directory string, args ...string) mcpclient.Config {
	env := make(map[string]string)
	for _, entry := range driverEnvironment(os.Environ()) {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	return mcpclient.Config{Stdio: &mcpclient.StdioConfig{
		Executable: binary, Args: args, Dir: directory, Env: env, ReplaceEnvironment: true,
	}}
}

func newProxyConfig(binary, endpoint string) (mcpclient.Config, error) {
	if !filepath.IsAbs(binary) || endpoint == "" {
		return mcpclient.Config{}, errors.New("desktop: verified absolute binary and service endpoint required")
	}
	directory, err := os.Getwd()
	if err != nil {
		return mcpclient.Config{}, errors.New("desktop: working directory unavailable")
	}
	// --embedded disables proxy autolaunch in the pinned release. It changes
	// neither the shared daemon's TCC attribution nor its authority mode.
	return driverMCPConfig(binary, directory, "mcp", "--socket", endpoint, "--embedded"), nil
}
