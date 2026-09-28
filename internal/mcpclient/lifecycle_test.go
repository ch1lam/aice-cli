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
)

func TestStdioBlockedWriteIsCancelable(t *testing.T) {
	config := stdioConfig(t)
	config.Stdio.Env["AICE_MCP_TEST_MODE"] = "stop-reading"
	config.CallTimeout = 200 * time.Millisecond
	c, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	start := time.Now()
	result, err := c.Call(t.Context(), "echo", json.RawMessage(`{"large":"`+strings.Repeat("x", 900000)+`"}`))
	if !errors.Is(err, context.DeadlineExceeded) || result.State != llm.ExecutionUnknown || time.Since(start) > 5*time.Second {
		t.Fatalf("blocked pipe result=%+v err=%v elapsed=%v", result, err, time.Since(start))
	}
	_ = c.Close()
	select {
	case <-c.child.done:
	default:
		t.Fatal("owned child not reaped")
	}
}

func TestQueuedCancellationAndConcurrentClose(t *testing.T) {
	started := make(chan struct{})
	f := &httpFixture{onCall: func(w http.ResponseWriter, r *http.Request, _ fixtureRequest) { close(started); <-r.Context().Done() }}
	c := openFixture(t, f, nil)
	done := make(chan Result, 1)
	go func() { result, _ := c.Call(t.Context(), "echo", json.RawMessage(`{}`)); done <- result }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("call did not start")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	queued, err := c.Call(ctx, "echo", json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) || queued.State != llm.ExecutionNotDispatched {
		t.Fatalf("queued: %+v %v", queued, err)
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on active request")
	}
	select {
	case result := <-done:
		if result.State != llm.ExecutionUnknown {
			t.Fatalf("active: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not finish")
	}
	if f.calls.Load() != 1 {
		t.Fatalf("dispatched %d times", f.calls.Load())
	}
}

func TestHTTPAuthDoesNotCrossRedirectOrLeakErrors(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer destination.Close()
	f := &httpFixture{onCall: func(w http.ResponseWriter, r *http.Request, _ fixtureRequest) {
		if r.Header.Get("Authorization") != "Bearer secret-value" {
			t.Error("explicit credential was not sent")
		}
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = fmt.Fprint(w, "secret-value")
	}}
	c := openFixture(t, f, func(c *Config) { c.HTTP.Headers = map[string]string{"Authorization": "Bearer secret-value"} })
	_, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`))
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != 307 || strings.Contains(err.Error(), "secret-value") || leaked.Load() != 0 {
		t.Fatalf("error=%v destination calls=%d", err, leaked.Load())
	}
}

func TestWireNotificationInvalidatesBeforeCatalogResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req fixtureRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if req.Method == "tools/list" {
			_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", req.ID, fixtureResult(req))
	}))
	defer server.Close()
	c, err := Open(t.Context(), Config{HTTP: &HTTPConfig{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	catalog, err := c.Tools(t.Context())
	if err != nil || catalog.Complete || !strings.Contains(catalog.Notice, "changed") || c.ToolGeneration() <= catalog.Generation {
		t.Fatalf("catalog=%+v err=%v generation=%d", catalog, err, c.ToolGeneration())
	}
}

func TestConfigRejectsImplicitAndReplayableConnections(t *testing.T) {
	for _, config := range []Config{
		{}, {Stdio: &StdioConfig{Executable: "relative", Dir: "relative"}},
		{HTTP: &HTTPConfig{Endpoint: "http://example.com/mcp"}},
		{HTTP: &HTTPConfig{Endpoint: "https://user:password@example.com/mcp"}},
		{HTTP: &HTTPConfig{Endpoint: "https://example.com/mcp", Headers: map[string]string{"Idempotency-Key": "replay"}}},
		{HTTP: &HTTPConfig{Endpoint: "https://example.com/mcp", Headers: map[string]string{"Authorization": "bad\r\nvalue"}}},
		{HTTP: &HTTPConfig{Endpoint: "https://example.com/mcp"}, Limits: Limits{Pages: maxPages + 1}},
	} {
		if _, err := Open(t.Context(), config); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid config error: %v", err)
		}
	}
}

func TestUnsupportedReverseRequestDoesNotHang(t *testing.T) {
	rejected := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req fixtureRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "" && len(req.Error) > 0 {
			rejected <- req.Error
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "initialize" {
			var params struct {
				Capabilities map[string]json.RawMessage `json:"capabilities"`
			}
			_ = json.Unmarshal(req.Params, &params)
			for _, unsupported := range []string{"roots", "sampling", "elicitation"} {
				if _, advertised := params.Capabilities[unsupported]; advertised {
					t.Errorf("advertised %s", unsupported)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		result := fixtureResult(req)
		if req.Method == "tools/call" {
			_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"reverse1\",\"method\":\"sampling/createMessage\",\"params\":{\"messages\":[],\"maxTokens\":1}}\n\n")
			result = `{"content":[{"type":"text","text":"done"}]}`
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", req.ID, result)
	}))
	defer server.Close()
	c, err := Open(t.Context(), Config{HTTP: &HTTPConfig{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`))
	if err != nil || result.State != llm.ExecutionReturned {
		t.Fatalf("call outcome=%s err=%v", result.State, err)
	}
	select {
	case raw := <-rejected:
		var failure struct {
			Code int `json:"code"`
		}
		// The pinned SDK uses -31001 for a known but unsupported method.
		if json.Unmarshal(raw, &failure) != nil || failure.Code != -31001 {
			t.Fatalf("reverse response: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reverse request was not rejected")
	}
}

func TestEscapedArgumentsStillTrackDispatch(t *testing.T) {
	f := &httpFixture{onCall: func(w http.ResponseWriter, r *http.Request, _ fixtureRequest) { w.WriteHeader(http.StatusUnauthorized) }}
	c := openFixture(t, f, nil)
	args := json.RawMessage(`{"text":"` + strings.Repeat("<", 300000) + `"}`)
	result, err := c.Call(t.Context(), "echo", args)
	var failure *HTTPError
	if result.State != llm.ExecutionUnknown || !errors.As(err, &failure) || failure.StatusCode != 401 || f.calls.Load() != 1 {
		t.Fatalf("outcome=%s err=%v calls=%d", result.State, err, f.calls.Load())
	}
}
