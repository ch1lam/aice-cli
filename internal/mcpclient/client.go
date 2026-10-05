package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client struct {
	session            *mcp.ClientSession
	info               Info
	limits             Limits
	timeout            time.Duration
	receipts           receipts
	gate               chan struct{}
	closed             chan struct{}
	once               sync.Once
	closeErr           error
	cancel             context.CancelFunc
	child              *childProcess
	http               *http.Transport
	toolGeneration     atomic.Uint64
	resourceGeneration atomic.Uint64
}

// Open initializes one connection. Canceling ctx bounds initialization; after
// success the application owns the connection and must Close it explicitly.
func Open(ctx context.Context, config Config) (*Client, error) {
	if (config.Stdio == nil) == (config.HTTP == nil) || config.ConnectTimeout < 0 || config.CallTimeout < 0 {
		return nil, ErrConfig
	}
	// Exact tiers supported by the SDK. Review with SDK upgrades.
	switch config.ProtocolVersion {
	case "", "2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05":
	default:
		return nil, ErrConfig
	}
	if err := normalizeLimits(&config.Limits); err != nil {
		return nil, err
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 15 * time.Second
	}
	if config.CallTimeout == 0 {
		config.CallTimeout = 60 * time.Second
	}
	ctx, cancelConnect := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancelConnect()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	c := &Client{limits: config.Limits, timeout: config.CallTimeout, cancel: cancel,
		gate: make(chan struct{}, 1), closed: make(chan struct{})}
	c.toolGeneration.Store(1)
	c.resourceGeneration.Store(1)
	// Invalidate before forwarding a notification frame to the SDK, so a
	// following response cannot race its asynchronously scheduled handler.
	c.receipts.onNotification = func(method string) {
		switch method {
		case "notifications/tools/list_changed":
			c.toolGeneration.Add(1)
		case "notifications/resources/list_changed":
			c.resourceGeneration.Add(1)
		}
	}
	var transport mcp.Transport
	var err error
	if config.Stdio != nil {
		transport, c.child, err = openStdio(*config.Stdio, c.limits.MessageBytes, &c.receipts)
	} else {
		transport, c.http, err = openHTTP(*config.HTTP, lifetime, c.limits.MessageBytes, &c.receipts)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	stopChild := c.cancelChildOn(ctx)
	defer stopChild()
	protocol := mcp.NewClient(&mcp.Implementation{Name: "aice", Version: buildinfo.Version}, &mcp.ClientOptions{
		Capabilities:               &mcp.ClientCapabilities{},
		Logger:                     slog.New(slog.NewTextHandler(io.Discard, nil)),
		ToolListChangedHandler:     func(context.Context, *mcp.ToolListChangedRequest) {},
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) {},
		// The Loop owns follow-up and retries, including input-required results.
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	// Retain the original connection only for failed-initialization cleanup.
	// Returning it unchanged preserves SDK-private sessionUpdated hooks.
	captured := &captureTransport{Transport: transport}
	c.receipts.begin("connect")
	c.session, err = protocol.Connect(ctx, captured, &mcp.ClientSessionOptions{ProtocolVersion: config.ProtocolVersion})
	received := c.receipts.finish()
	if err != nil {
		_ = c.Close()
		if captured.connection != nil {
			_ = captured.connection.Close()
		}
		return nil, operationError(ctx, received, err)
	}
	init := c.session.InitializeResult()
	if init == nil || init.ServerInfo == nil || init.Capabilities == nil ||
		(config.ProtocolVersion != "" && init.ProtocolVersion != config.ProtocolVersion) {
		_ = c.Close()
		return nil, ErrProtocol
	}
	c.info = Info{Name: init.ServerInfo.Name, Version: init.ServerInfo.Version,
		ProtocolVersion: init.ProtocolVersion, Instructions: init.Instructions,
		Tools: init.Capabilities.Tools != nil, Resources: init.Capabilities.Resources != nil,
		Prompts: init.Capabilities.Prompts != nil}
	return c, nil
}

type captureTransport struct {
	mcp.Transport
	connection mcp.Connection
}

func (t *captureTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	t.connection = connection
	return connection, err
}

func normalizeLimits(limits *Limits) error {
	if limits.MessageBytes == 0 {
		limits.MessageBytes = defaultMessageBytes
	}
	for _, field := range []struct {
		value   *int
		maximum int
	}{
		{&limits.MessageBytes, maxMessageBytes}, {&limits.CatalogBytes, maxCatalogBytes},
		{&limits.CatalogItems, maxCatalogItems}, {&limits.Pages, maxPages},
	} {
		if *field.value < 0 || *field.value > field.maximum {
			return ErrConfig
		}
		if *field.value == 0 {
			*field.value = field.maximum
		}
	}
	return nil
}

func (c *Client) Info() Info                 { return c.info }
func (c *Client) ToolGeneration() uint64     { return c.toolGeneration.Load() }
func (c *Client) ResourceGeneration() uint64 { return c.resourceGeneration.Load() }

// StdioPID identifies this connection's owned child for diagnostics. It is zero
// for HTTP and does not establish that the child is still alive.
func (c *Client) StdioPID() int {
	if c.child == nil {
		return 0
	}
	return c.child.cmd.Process.Pid
}

func (c *Client) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.cancel()
		// Unblock pipe writes before the SDK waits for in-flight requests.
		if c.child != nil {
			c.closeErr = c.child.Close()
		}
		if c.session != nil {
			if err := c.session.Close(); err != nil {
				c.closeErr = ErrTransport
			}
		}
		if c.http != nil {
			c.http.CloseIdleConnections()
		}
	})
	return c.closeErr
}

func (c *Client) acquire(ctx context.Context) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	case <-c.closed:
		cancel()
		return nil, nil, ErrClosed
	}
	release := func() { cancel(); <-c.gate }
	select {
	case <-c.closed:
		release()
		return nil, nil, ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	stopChild := c.cancelChildOn(ctx)
	return ctx, func() { stopChild(); release() }, nil
}

// os.Pipe writes are not context-aware. Canceling an active stdio operation
// closes its owned pipes/process, so even a peer that stops reading cannot
// hold a writer forever. Reconnection, if desired, is an application decision.
func (c *Client) cancelChildOn(ctx context.Context) func() {
	if c.child == nil {
		return func() {}
	}
	stop := context.AfterFunc(ctx, func() { _ = c.child.Close() })
	return func() { stop() }
}

func operationError(ctx context.Context, received receipt, err error) error {
	if received.limited {
		return ErrLimit
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if received.httpStatus != 0 {
		return &HTTPError{StatusCode: received.httpStatus}
	}
	if received.rpcError || len(received.result) > 0 {
		return ErrProtocol
	}
	if err != nil {
		return ErrTransport
	}
	return ErrProtocol
}

func validName(name string) bool {
	return name != "" && len(name) <= 4096 && utf8.ValidString(name) && !strings.ContainsAny(name, "\x00\r\n")
}

func jsonObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 1 && raw[0] == '{' && json.Valid(raw) && utf8.Valid(raw)
}

// Call performs exactly one SDK call. It neither discovers a tool nor grants
// execution authority; the caller must check the current identity and Guard.
func (c *Client) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	return c.CallChecked(ctx, name, arguments, nil)
}

// CallChecked additionally revalidates application-owned assumptions after
// waiting for this connection's queue and immediately before protocol dispatch.
// check must be local, bounded, side-effect-free and must not reenter this client.
func (c *Client) CallChecked(ctx context.Context, name string, arguments json.RawMessage, check func(context.Context) error) (Result, error) {
	result := Result{State: llm.ExecutionNotDispatched}
	if !validName(name) || len(arguments) > maxArgumentBytes || !jsonObject(arguments) {
		return result, ErrConfig
	}
	if !c.info.Tools {
		return result, ErrUnsupported
	}
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if check != nil {
		if err := check(ctx); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	c.receipts.begin("tools/call")
	_, callErr := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	received := c.receipts.finish()
	return decodeResult(ctx, received, callErr, "content")
}

// ReadResource explicitly reads one URI from this server; links in its result
// are not followed. Resource reads require application authorization too.
func (c *Client) ReadResource(ctx context.Context, uri string) (Result, error) {
	return c.ReadResourceChecked(ctx, uri, nil)
}

// ReadResourceChecked revalidates after the serialized connection queue, just
// like CallChecked. A rejected check never dispatches or replays the read.
func (c *Client) ReadResourceChecked(ctx context.Context, uri string, check func(context.Context) error) (Result, error) {
	result := Result{State: llm.ExecutionNotDispatched}
	if !validName(uri) {
		return result, ErrConfig
	}
	if !c.info.Resources {
		return result, ErrUnsupported
	}
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if check != nil {
		if err := check(ctx); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	c.receipts.begin("resources/read")
	read, readErr := c.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if read != nil {
		// Explicit reads need fresh, bounded wire evidence, not a decoded cache hit.
		read.TTLMs = 0
	}
	return decodeResult(ctx, c.receipts.finish(), readErr, "contents")
}

func decodeResult(ctx context.Context, received receipt, callErr error, contentKey string) (Result, error) {
	result := Result{State: llm.ExecutionNotDispatched}
	if received.attempted {
		result.State = llm.ExecutionUnknown
	}
	if len(received.result) == 0 || received.rpcError {
		if received.rpcError {
			result.State, result.IsError = llm.ExecutionReturned, true
		}
		if received.limited {
			result.Loss = "The response exceeded the configured message storage limit."
		}
		return result, operationError(ctx, received, callErr)
	}
	result.State = llm.ExecutionReturned
	var wire map[string]json.RawMessage
	var blocks []json.RawMessage
	if json.Unmarshal(received.result, &wire) == nil && string(wire["resultType"]) == `"input_required"` {
		result.IsError = true
		result.Loss = "The server requested an unsupported follow-up exchange; the operation was not replayed."
		return result, ErrUnsupported
	}
	if !jsonObject(received.result) || json.Unmarshal(received.result, &wire) != nil ||
		json.Unmarshal(wire[contentKey], &blocks) != nil || bytes.Equal(bytes.TrimSpace(wire[contentKey]), []byte("null")) ||
		(len(wire["isError"]) > 0 && json.Unmarshal(wire["isError"], &result.IsError) != nil) {
		result.IsError = true
		result.Loss = "The response was received but its malformed result could not be retained."
		return result, ErrProtocol
	}
	for _, raw := range blocks {
		result.Content = append(result.Content, decodeBlock(raw, contentKey == "contents"))
	}
	result.StructuredContent = wire["structuredContent"]
	// Raw frames remain authoritative when the SDK rejects an unfamiliar
	// content kind or would round arbitrary JSON numbers. The adapter will
	// explicitly label unsupported model input, retaining the source block.
	return result, nil
}
