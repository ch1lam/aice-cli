package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type fixtureRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Error  json.RawMessage `json:"error"`
}

const fixtureInit = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true},"resources":{"listChanged":true},"prompts":{}},"serverInfo":{"name":"fixture","version":"1"},"instructions":"Untrusted server instructions"}`

func fixtureResult(req fixtureRequest) string {
	switch req.Method {
	case "initialize":
		return fixtureInit
	case "tools/list":
		if bytes.Contains(req.Params, []byte("second")) {
			return `{"tools":[{"name":"second","inputSchema":{"type":"object"}}]}`
		}
		return `{"tools":[{"name":"echo","description":"Echo values","inputSchema":{"type":"object","properties":{"n":{"type":"integer","default":9007199254740993}}}}],"nextCursor":"second"}`
	case "tools/call":
		var params struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return `{"content":[{"type":"text","text":"before"},{"type":"image","data":"AQID","mimeType":"image/png"},{"type":"text","text":"after"},{"type":"resource_link","uri":"fixture://one","name":"one"},{"type":"audio","data":"BAUG","mimeType":"audio/wav"},{"type":"future","value":9007199254740993}],"structuredContent":` + string(params.Arguments) + `,"isError":true}`
	case "resources/list":
		return `{"resources":[{"uri":"fixture://one","name":"one","mimeType":"text/plain"}]}`
	case "resources/read":
		return `{"contents":[{"uri":"fixture://one","text":"resource body","mimeType":"text/plain"}]}`
	}
	return `{}`
}

// A subprocess fixture validates actual pipes, process ownership and environment
// isolation. It is not an external-service interoperability or platform test.
func TestMCPServerHelper(t *testing.T) {
	if os.Getenv("AICE_MCP_TEST_HELPER") != "1" {
		return
	}
	if os.Getenv("AICE_MCP_SECRET_TEST") != "" {
		os.Exit(3)
	}
	if os.Getenv("AICE_MCP_TEST_MODE") == "hang" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxMessageBytes)
	for scanner.Scan() {
		var req fixtureRequest
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(4)
		}
		if len(req.ID) == 0 {
			continue
		}
		fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n", req.ID, fixtureResult(req))
		if req.Method == "initialize" && os.Getenv("AICE_MCP_TEST_MODE") == "stop-reading" {
			time.Sleep(time.Minute)
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func stdioConfig(t *testing.T) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Stdio: &StdioConfig{Executable: executable,
		Args: []string{"-test.run=^TestMCPServerHelper$"}, Dir: t.TempDir(),
		Env: map[string]string{"AICE_MCP_TEST_HELPER": "1"}}, ConnectTimeout: 10 * time.Second}
}

func TestStdioRoundTrip(t *testing.T) {
	t.Setenv("AICE_MCP_SECRET_TEST", "must-not-inherit")
	c, err := Open(t.Context(), stdioConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	assertRoundTrip(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`)); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
}

func assertRoundTrip(t *testing.T, c *Client) {
	t.Helper()
	if got := c.Info(); got.Name != "fixture" || !got.Tools || !got.Resources || !got.Prompts {
		t.Fatalf("info: %+v", got)
	}
	catalog, err := c.Tools(t.Context())
	if err != nil || !catalog.Complete || len(catalog.Items) != 2 {
		t.Fatalf("tools: %+v, %v", catalog, err)
	}
	if !bytes.Contains(catalog.Items[0].InputSchema, []byte("9007199254740993")) {
		t.Fatalf("schema number rounded: %s", catalog.Items[0].InputSchema)
	}
	args := json.RawMessage(`{"n":9007199254740993,"decimal":1.234567890123456789}`)
	result, err := c.Call(t.Context(), "echo", args)
	if err != nil || result.State != llm.ExecutionReturned || !result.IsError || len(result.Content) != 6 || !bytes.Equal(result.StructuredContent, args) {
		t.Fatalf("call: %+v, %v", result, err)
	}
	for i, kind := range []BlockKind{BlockText, BlockImage, BlockText, BlockResourceLink, BlockAudio, BlockUnsupported} {
		if result.Content[i].Kind != kind {
			t.Fatalf("block %d reordered: %+v", i, result.Content[i])
		}
	}
	if !bytes.Contains(result.Content[5].Unsupported, []byte("9007199254740993")) {
		t.Fatal("unsupported block lost its exact source")
	}
	resources, err := c.Resources(t.Context())
	if err != nil || !resources.Complete || len(resources.Items) != 1 {
		t.Fatalf("resources: %+v, %v", resources, err)
	}
	read, err := c.ReadResource(t.Context(), resources.Items[0].URI)
	if err != nil || read.State != llm.ExecutionReturned || len(read.Content) != 1 || read.Content[0].Text != "resource body" {
		t.Fatalf("resource read: %+v, %v", read, err)
	}
}

type httpFixture struct {
	calls  atomic.Int32
	gets   chan string
	onCall func(http.ResponseWriter, *http.Request, fixtureRequest)
	result func(fixtureRequest) string
	sse    bool
}

func (f *httpFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if f.gets != nil {
			select {
			case f.gets <- r.Header.Get("Mcp-Protocol-Version"):
			default:
			}
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req fixtureRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Method == "tools/call" {
		f.calls.Add(1)
		if f.onCall != nil {
			f.onCall(w, r, req)
			return
		}
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", "fixture-session")
	}
	result := fixtureResult(req)
	if f.result != nil {
		result = f.result(req)
	}
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\r\n\r\n", req.ID, result)
	} else {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}", req.ID, result)
	}
}

func openFixture(t *testing.T, fixture *httpFixture, change func(*Config)) *Client {
	t.Helper()
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	config := Config{HTTP: &HTTPConfig{Endpoint: server.URL}, CallTimeout: 5 * time.Second}
	if change != nil {
		change(&config)
	}
	c, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestHTTPRoundTrip(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			f := &httpFixture{sse: sse, gets: make(chan string, 1)}
			c := openFixture(t, f, nil)
			assertRoundTrip(t, c)
			select {
			case version := <-f.gets:
				if version != "2025-11-25" {
					t.Fatalf("lost session negotiation: %q", version)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SDK standalone SSE was not started")
			}
		})
	}
}

func TestHTTPNoReplay(t *testing.T) {
	for _, mode := range []string{"disconnect", "unauthorized", "redirect", "canceled", "limit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := &httpFixture{}
			f.onCall = func(w http.ResponseWriter, r *http.Request, req fixtureRequest) {
				switch mode {
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
				case "redirect":
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "canceled":
					cancel()
					<-r.Context().Done()
				case "limit":
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, strings.Repeat("x", 2048))
				}
			}
			c := openFixture(t, f, func(c *Config) {
				if mode == "limit" {
					c.Limits.MessageBytes = 1024
				}
			})
			result, err := c.Call(ctx, "echo", json.RawMessage(`{}`))
			if err == nil || result.State != llm.ExecutionUnknown || f.calls.Load() != 1 {
				t.Fatalf("outcome=%+v err=%v dispatches=%d", result, err, f.calls.Load())
			}
			if mode == "limit" && (result.Loss == "" || !errors.Is(err, ErrLimit)) {
				t.Fatalf("missing loss: %+v %v", result, err)
			}
		})
	}
}

func TestInvalidAndCanceledBeforeDispatch(t *testing.T) {
	f := &httpFixture{}
	c := openFixture(t, f, nil)
	for _, args := range []string{"", "[]", "null", "{broken"} {
		result, err := c.Call(t.Context(), "echo", json.RawMessage(args))
		if !errors.Is(err, ErrConfig) || result.State != llm.ExecutionNotDispatched {
			t.Fatalf("%q: %+v %v", args, result, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := c.Call(ctx, "echo", json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) || result.State != llm.ExecutionNotDispatched || f.calls.Load() != 0 {
		t.Fatalf("%+v %v calls=%d", result, err, f.calls.Load())
	}
}

func TestHungChildInitializationAndClose(t *testing.T) {
	config := stdioConfig(t)
	config.Stdio.Env["AICE_MCP_TEST_MODE"] = "hang"
	config.ConnectTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := Open(t.Context(), config)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("hung initialization: %v in %v", err, time.Since(start))
	}
}
