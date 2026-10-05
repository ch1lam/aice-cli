package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtureDiscover = `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{},"resources":{},"prompts":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"fixture","version":"1"}},"instructions":"Untrusted server instructions"}`

func TestCurrentProtocolDiscoveryAndFreshReads(t *testing.T) {
	for _, pin := range []string{"", "2026-07-28"} {
		for _, sse := range []bool{false, true} {
			t.Run(fmt.Sprintf("pin=%s/sse=%v", pin, sse), func(t *testing.T) {
				var discoveries, lists, reads atomic.Int32
				f := &httpFixture{sse: sse, result: func(req fixtureRequest) string {
					var params struct {
						Meta map[string]json.RawMessage `json:"_meta"`
					}
					if json.Unmarshal(req.Params, &params) != nil || string(params.Meta["io.modelcontextprotocol/protocolVersion"]) != `"2026-07-28"` {
						t.Error("missing negotiated protocol metadata", req.Method)
					}
					switch req.Method {
					case "server/discover":
						discoveries.Add(1)
						return fixtureDiscover
					case "initialize":
						t.Error("modern server used initialize")
					case "tools/list", "resources/list":
						lists.Add(1)
					case "resources/read":
						reads.Add(1)
					}
					// A server TTL must not hide explicit refreshes or lose exact
					// schemas through the SDK's decoded-result cache.
					return strings.Replace(fixtureResult(req), "{", `{"ttlMs":60000,`, 1)
				}}
				c := openFixture(t, f, func(c *Config) { c.ProtocolVersion = pin })
				if c.Info().ProtocolVersion != "2026-07-28" {
					t.Fatal("current protocol was not negotiated")
				}
				for range 2 {
					assertRoundTrip(t, c)
				}
				if discoveries.Load() != 1 || lists.Load() != 6 || reads.Load() != 2 || f.calls.Load() != 2 {
					t.Fatal("unexpected discovery/cache/replay behavior", discoveries.Load(), lists.Load(), reads.Load(), f.calls.Load())
				}
			})
		}
	}
}

func TestCurrentProtocolInputRequiredDoesNotReplay(t *testing.T) {
	f := &httpFixture{result: func(req fixtureRequest) string {
		switch req.Method {
		case "server/discover":
			return fixtureDiscover
		case "tools/call":
			return `{"resultType":"input_required","inputRequests":{},"requestState":"opaque"}`
		default:
			return fixtureResult(req)
		}
	}}
	c := openFixture(t, f, nil)
	result, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnsupported) || result.State != llm.ExecutionReturned || !result.IsError || f.calls.Load() != 1 {
		t.Fatal("input-required result was replayed or lost its outcome", result, err, f.calls.Load())
	}
}

func TestCurrentProtocolSubscriptions(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "subscriptions", Version: "1"}, nil)
	addTool := func(name string) {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
			})
	}
	addTool("one")
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	host := httptest.NewServer(handler)
	defer host.Close()
	c, err := Open(t.Context(), Config{HTTP: &HTTPConfig{Endpoint: host.URL}, ConnectTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Info().ProtocolVersion != "2026-07-28" {
		t.Fatal("subscription test did not negotiate current protocol")
	}
	before, err := c.Tools(t.Context())
	if err != nil || !before.Complete || len(before.Items) != 1 {
		t.Fatal("initial catalog", before, err)
	}
	addTool("two")
	deadline := time.After(3 * time.Second)
	ticks := time.NewTicker(10 * time.Millisecond)
	defer ticks.Stop()
	for c.ToolGeneration() == before.Generation {
		select {
		case <-deadline:
			t.Fatal("SDK subscription did not invalidate the catalog")
		case <-ticks.C:
		}
	}
	after, err := c.Tools(t.Context())
	if err != nil || !after.Complete || len(after.Items) != 2 {
		t.Fatal("changed catalog", after, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal("subscription cleanup", err)
	}
}
