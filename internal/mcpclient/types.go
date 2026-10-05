// Package mcpclient owns one MCP connection. It has no model selection, Guard,
// configuration discovery, desktop state, or automatic tool replay policy.
package mcpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// HTTPError provides status for authentication/status UI without retaining an
// endpoint, header, remote error body, or underlying error containing secrets.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("MCP HTTP request failed (status %d)", e.StatusCode)
}
func (e *HTTPError) Unwrap() error { return ErrTransport }

var (
	ErrConfig      = errors.New("invalid MCP connection configuration")
	ErrClosed      = errors.New("MCP connection closed")
	ErrTransport   = errors.New("MCP transport failed; connection details omitted")
	ErrProtocol    = errors.New("invalid MCP protocol response")
	ErrLimit       = errors.New("MCP response exceeded its storage limit")
	ErrUnsupported = errors.New("MCP server does not advertise this capability")
)

// Config describes an already authorized connection. Exactly one transport is
// required. Open does not discover tools, install executables, or authorize use.
type Config struct {
	Stdio          *StdioConfig
	HTTP           *HTTPConfig
	ConnectTimeout time.Duration
	CallTimeout    time.Duration
	Limits         Limits
	// ProtocolVersion pins an exact supported tier; empty uses SDK negotiation.
	ProtocolVersion string
}

type StdioConfig struct {
	Executable string // absolute executable; never interpreted by a shell
	Args       []string
	Dir        string            // explicit absolute working directory
	Env        map[string]string // explicit additions to the small base allowlist
	// ReplaceEnvironment supplies the complete environment instead of adding
	// to the base allowlist. Only Go-required OS variables may be added.
	ReplaceEnvironment bool
}

type HTTPConfig struct {
	Endpoint string            // HTTPS, or HTTP on a literal loopback address/localhost
	Headers  map[string]string // resolved by the caller, never included in errors
	// Authorization reads the caller-owned current header without I/O. It must
	// be concurrency-safe and cannot be combined with a static Authorization.
	// Refresh/consent happen before dispatch, never in this transport.
	Authorization func() string
}

// Limits are hard allocation/storage limits, not model context budgets. Zero
// uses the defaults. MessageBytes defaults to 16 MiB with a 24 MiB ceiling;
// other fields default to their ceilings.
type Limits struct {
	MessageBytes int
	CatalogBytes int
	CatalogItems int
	Pages        int
}

const (
	defaultMessageBytes = 16 << 20
	maxMessageBytes     = 24 << 20
	maxCatalogBytes     = 4 << 20
	maxCatalogItems     = 2000
	maxPages            = 20
	maxArgumentBytes    = 1 << 20
)

// Info is usage data supplied by the server, never an instruction or grant.
type Info struct {
	Name, Version, ProtocolVersion, Instructions string
	Tools, Resources, Prompts                    bool
}

// Tool retains exact schemas and SDK-normalized annotations (untrusted hints).
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
}

// Catalog owns its values; no slice or schema aliases mutable client state.
// Generation is invalidated by list_changed. Complete=false is never an empty
// success: Notice explains a bound, stale pagination, or partial failure.
type Catalog[T any] struct {
	Items      []T
	Generation uint64
	Complete   bool
	Notice     string
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type BlockKind string

const (
	BlockText         BlockKind = "text"
	BlockImage        BlockKind = "image"
	BlockAudio        BlockKind = "audio"
	BlockResourceLink BlockKind = "resource_link"
	BlockResourceText BlockKind = "resource_text"
	BlockResourceBlob BlockKind = "resource_blob"
	BlockUnsupported  BlockKind = "unsupported"
)

// Block is a transport-neutral result value. Resource supplies provenance for
// links and embedded data. Unsupported marks SDK values the adapter cannot use.
type Block struct {
	Kind     BlockKind
	Text     string
	Data     []byte
	MIMEType string
	Resource Resource
}

// Result contains ordered content and exact structured JSON. The tool adapter
// maps these values into model/history content, validates media and explicitly
// reports anything it cannot retain.
// This is transient source data, not another durable transcript.
type Result struct {
	Content           []Block
	StructuredContent json.RawMessage
	IsError           bool
	State             llm.ExecutionState
	Loss              string
}
