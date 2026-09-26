// Package desktop owns Cua connections and desktop operation lifetimes.
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
)

const (
	DriverVersion   = "0.29.1"
	ProtocolVersion = "2025-06-18"
	maxMessageBytes = 24 * 1024 * 1024
	connectTimeout  = 15 * time.Second
	callTimeout     = 30 * time.Second
)

// Reply retains domain failures and observations independently of transport
// errors. A transport error after dispatch never establishes that input failed.
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

// beforeDispatchError is reserved for failures established before CallTool.
// Other transport errors cannot prove whether the native action happened.
type beforeDispatchError struct{ error }

func (e beforeDispatchError) Unwrap() error { return e.error }

// client is private: callers cannot expose arbitrary Driver tools to the model.
type client struct {
	session *mcp.ClientSession
	tools   map[string]json.RawMessage
}

func connect(ctx context.Context, transport mcp.Transport) (*client, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	c := mcp.NewClient(&mcp.Implementation{Name: "aice", Version: buildinfo.Version}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{},
		Logger:       slog.New(slog.DiscardHandler),
	})
	// The SDK normally proposes its newest legacy version. Pin the reviewed
	// initialize tier without adding modern per-request discovery metadata.
	c.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if init, ok := req.(*mcp.InitializeRequest); ok {
				init.Params.ProtocolVersion = ProtocolVersion
			}
			return next(ctx, method, req)
		}
	})
	tracked := &trackedTransport{Transport: transport}
	session, err := c.Connect(ctx, tracked, nil)
	if err != nil {
		if tracked.connection != nil {
			_ = tracked.connection.Close()
		}
		return nil, fmt.Errorf("desktop: initialize: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = session.Close()
		}
	}()
	init := session.InitializeResult()
	if init.ProtocolVersion != ProtocolVersion || init.ServerInfo == nil || init.ServerInfo.Name != "cua-driver" || init.ServerInfo.Version != DriverVersion {
		return nil, errors.New("desktop: incompatible Driver identity or protocol")
	}
	result := &client{session: session, tools: make(map[string]json.RawMessage)}
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; ; page++ {
		if page >= 16 {
			return nil, errors.New("desktop: tool discovery exceeds page limit")
		}
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("desktop: discover tools: %w", err)
		}
		for _, tool := range list.Tools {
			if tool == nil || tool.Name == "" {
				return nil, errors.New("desktop: invalid tool descriptor")
			}
			if _, exists := result.tools[tool.Name]; exists {
				return nil, errors.New("desktop: duplicate tool descriptor")
			}
			if len(result.tools) >= 256 {
				return nil, errors.New("desktop: too many tool descriptors")
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("desktop: tool schema: %w", err)
			}
			result.tools[tool.Name] = schema
		}
		cursor = list.NextCursor
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return nil, errors.New("desktop: repeated discovery cursor")
		}
		seen[cursor] = true
	}
	result.tools, err = reviewedMacTools(result.tools)
	if err != nil {
		return nil, err
	}
	ok = true
	return result, nil
}

// call sends exactly once. Neither domain errors nor EOF/cancellation are retried.
func (c *client) call(ctx context.Context, name string, arguments any) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, beforeDispatchError{err}
	}
	if _, ok := c.tools[name]; !ok {
		return Reply{}, beforeDispatchError{fmt.Errorf("desktop: capability %s unavailable", name)}
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return Reply{}, fmt.Errorf("desktop: %s transport failed; dispatched outcome may be unknown: %w", name, err)
	}
	reply := Reply{IsError: result.IsError}
	if result.StructuredContent != nil {
		reply.Structured, err = json.Marshal(result.StructuredContent)
		if err != nil {
			return reply, fmt.Errorf("desktop: invalid structured result: %w", err)
		}
	}
	for _, part := range result.Content {
		switch part := part.(type) {
		case *mcp.TextContent:
			reply.Text = append(reply.Text, part.Text)
		case *mcp.ImageContent:
			reply.Images = append(reply.Images, Image{Data: part.Data, MIMEType: part.MIMEType})
		default:
			return reply, errors.New("desktop: unsupported Driver content; action outcome must be checked before continuing")
		}
	}
	return reply, nil
}

func (c *client) close() error { return c.session.Close() }

// Close a pipe whose peer stops reading when a write's deadline expires.
// The SDK checks ctx before Write but an OS pipe itself has no context.
type trackedTransport struct {
	mcp.Transport
	connection mcp.Connection
}

func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.connection = &cancelableConnection{Connection: c}
	return t.connection, nil
}

type cancelableConnection struct{ mcp.Connection }

func (c *cancelableConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.Connection.Close(); close(closed) })
	err := c.Connection.Write(ctx, message)
	if !stop() {
		<-closed
		return ctx.Err()
	}
	return err
}

// driverEnvironment deliberately excludes provider credentials, loader injection
// and inherited Cua authority overrides. Shared service preferences are untouched.
func driverEnvironment(environ []string) []string {
	var result []string
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USER", "LOGNAME", "PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY", "SYSTEMROOT", "WINDIR", "USERPROFILE", "LOCALAPPDATA", "APPDATA":
			result = append(result, entry)
		}
	}
	return append(result, "CUA_DRIVER_RS_TELEMETRY_ENABLED=false", "CUA_DRIVER_RS_UPDATE_CHECK=false", "CUA_DRIVER_PERMISSION_MODE=standard")
}

// processTransport adds bounded framing and child ownership to the SDK's stdio
// transport; JSON-RPC correlation, notifications and cancellation stay in the SDK.
type processTransport struct{ command *exec.Cmd }

func newProcessTransport(binary, endpoint string) (*processTransport, error) {
	if !filepath.IsAbs(binary) || endpoint == "" {
		return nil, errors.New("desktop: verified absolute binary and service endpoint required")
	}
	// On this pinned release, --embedded on the proxy disables automatic
	// standalone service launch. It does not change the connected daemon's TCC
	// attribution or mode. Never use --direct or set a claimed host bundle ID.
	cmd := exec.Command(binary, "mcp", "--socket", endpoint, "--embedded")
	cmd.Env = driverEnvironment(os.Environ())
	// Driver diagnostics can contain window text or input. Do not duplicate them
	// in the terminal, Session, or logs. Protocol failures carry content-free status.
	cmd.Stderr = io.Discard
	return &processTransport{command: cmd}, nil
}

func (t *processTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdout, err := t.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.command.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	if err := t.command.Start(); err != nil {
		_ = stdout.Close()
		_ = stdin.Close()
		return nil, err
	}
	p := &ownedProcess{cmd: t.command, stdout: stdout, stdin: stdin}
	transport := &mcp.IOTransport{Reader: &boundedReader{ReadCloser: p, limit: maxMessageBytes}, Writer: stdin}
	return transport.Connect(ctx)
}

type ownedProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stdin  io.WriteCloser
	once   sync.Once
	err    error
}

func (p *ownedProcess) Read(b []byte) (int, error) { return p.stdout.Read(b) }
func (p *ownedProcess) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case p.err = <-done:
		case <-timer.C:
			// Only this exact MCP child is owned, never its shared native service.
			_ = p.cmd.Process.Kill()
			p.err = <-done
		}
	})
	return p.err
}

// boundedReader enforces the NDJSON message limit before JSON/base64 allocation.
// It does not buffer whole frames or impose Scanner's default 64 KiB limit.
type boundedReader struct {
	io.ReadCloser
	limit, size int
}

func (r *boundedReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			r.size = 0
			continue
		}
		r.size++
		if r.size > r.limit {
			return 0, errors.New("desktop: MCP message exceeds size limit")
		}
	}
	return n, err
}
