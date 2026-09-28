package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type fixturePeer struct {
	calls       atomic.Int32
	initializes atomic.Int32
	pages       atomic.Int32
}

// A raw fake peer tests the actual legacy wire contract, independently of the
// SDK server implementation. It never starts Cua or reads desktop content.
func fakeTransport(t *testing.T, mode string) (mcpclient.Config, *fixturePeer) {
	t.Helper()
	schemas := schemaFixture(t)
	if strings.HasPrefix(mode, "linux-full") {
		schemas = linuxFullSchemaFixture(t)
		if mode == "linux-full-drift" {
			schemas["launch_app"] = json.RawMessage(`{"type":"object"}`)
		}
	}
	if strings.HasPrefix(mode, "linux-status") {
		schemas = linuxStatusFixture(t)
		if mode == "linux-status-drift" {
			schemas["check_permissions"] = json.RawMessage(`{"type":"object"}`)
		}
	}
	if strings.HasPrefix(mode, "windows-status") {
		schemas = windowsStatusFixture(t)
		if mode == "windows-status-drift" {
			schemas["check_permissions"] = json.RawMessage(`{"type":"object"}`)
		}
	}
	schemas["unreviewed_tool"] = json.RawMessage(`{"type":"object"}`)
	if mode == "missing-tool" {
		delete(schemas, "drag")
	}
	if mode == "changed-schema" {
		schemas["check_permissions"] = json.RawMessage(`{"type":"object"}`)
	}
	allNames := slices.Sorted(maps.Keys(schemas))
	state := &fixturePeer{}
	native := &fakeDriver{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		decoder, encoder := json.NewDecoder(r.Body), json.NewEncoder(w)
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if decoder.Decode(&request) != nil {
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch request.Method {
		case "initialize":
			state.initializes.Add(1)
			var params struct {
				Protocol string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Protocol != ProtocolVersion {
				t.Errorf("initialize protocol %s", params.Protocol)
			}
			version := DriverVersion
			if mode == "wrong-version" {
				version = "0.0.0"
			}
			result = map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]string{"name": "cua-driver", "version": version}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			state.pages.Add(1)
			var params struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(request.Params, &params)
			names := allNames[:3]
			next := "second"
			if params.Cursor != "" {
				names = allNames[3:]
				next = ""
			}
			if mode == "loop-pagination" {
				next = "second"
				names = nil
			}
			list := []any{}
			for _, name := range names {
				list = append(list, map[string]any{"name": name, "description": "synthetic descriptor", "inputSchema": schemas[name], "outputSchema": json.RawMessage(`{"type":"object","properties":{"n":{"default":9007199254740993}}}`), "annotations": json.RawMessage(`{"readOnlyHint":false}`)})
			}
			result = map[string]any{"tools": list, "nextCursor": next}
		case "tools/call":
			state.calls.Add(1)
			switch mode {
			case "managed-run":
				var params struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}
				if json.Unmarshal(request.Params, &params) != nil {
					t.Error("invalid managed fixture call")
					return
				}
				reply, err := native.call(r.Context(), params.Name, params.Arguments)
				if err != nil {
					t.Error("unexpected managed fixture operation", err)
					return
				}
				content := []any{}
				for _, text := range reply.Text {
					content = append(content, map[string]any{"type": "text", "text": text})
				}
				result = map[string]any{"structuredContent": reply.Structured, "isError": reply.IsError, "content": content}
			case "eof":
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			case "ordered-content":
				result = json.RawMessage(`{"structuredContent":{"n":9007199254740993},"isError":true,"content":[{"type":"text","text":"before"},{"type":"image","mimeType":"image/png","data":"AQID"},{"type":"text","text":"after"},{"type":"audio","mimeType":"audio/wav","data":"BAUG"},{"type":"future","n":9007199254740993}]}`)
			case "cancel":
				<-r.Context().Done()
				return
			default:
				result = map[string]any{"isError": true, "structuredContent": map[string]any{"effect": "partial", "secret": "synthetic"}, "content": []any{
					map[string]any{"type": "text", "text": "action dispatched; observation failed"},
					map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 100*1024))},
				}}
			}
		default:
			t.Errorf("unexpected RPC %s", request.Method)
			return
		}
		if mode == "changed-after-call" && request.Method == "tools/call" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			fmt.Fprintf(w, "data: %s\n\n", response)
			return
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
			return
		}
	}))
	t.Cleanup(server.Close)
	return mcpclient.Config{HTTP: &mcpclient.HTTPConfig{Endpoint: server.URL}}, state
}

func TestLegacyConnectionReuseAndContent(t *testing.T) {
	t.Parallel()
	transport, state := fakeTransport(t, "normal")
	c, err := connect(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if _, err := c.call(t.Context(), "unreviewed_tool", nil); err == nil || state.calls.Load() != 0 {
		t.Fatal("unreviewed upstream tool dispatched")
	}
	for range 2 {
		reply, err := c.call(t.Context(), "click", map[string]any{"pid": 1})
		if err != nil {
			t.Fatal(err)
		}
		if !reply.IsError || !bytes.Contains(reply.Structured, []byte(`"effect":"partial"`)) || len(reply.Images) != 1 || len(reply.Images[0].Data) != 100*1024 || len(reply.Text) != 1 {
			t.Fatalf("lost wire result: %d images, %s", len(reply.Images), reply.Structured)
		}
	}
	if state.initializes.Load() != 1 || state.pages.Load() != 2 || state.calls.Load() != 2 {
		t.Fatal("connection was not reused")
	}
}

func TestConnectionRejectsIncompatiblePeer(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"wrong-version", "loop-pagination", "missing-tool", "changed-schema"} {
		t.Run(mode, func(t *testing.T) {
			transport, state := fakeTransport(t, mode)
			if c, err := connect(t.Context(), transport); err == nil {
				_ = c.close()
				t.Fatal("accepted incompatible discovery")
			}
			if state.calls.Load() != 0 {
				t.Fatal("tool called before schema admission")
			}
		})
	}
}

func TestMutationTransportFailureNeverReplays(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"eof", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			transport, state := fakeTransport(t, mode)
			c, err := connect(t.Context(), transport)
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if _, err := c.call(ctx, "click", map[string]any{}); err == nil {
				t.Fatal("expected unknown outcome")
			}
			if state.calls.Load() != 1 {
				t.Fatalf("mutation calls=%d", state.calls.Load())
			}
			cancel()
			if _, err := c.call(ctx, "click", nil); !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("canceled call accepted or cancellation cause lost", err)
			}
			if state.calls.Load() != 1 {
				t.Fatal("canceled action dispatched")
			}
		})
	}
}

func TestDriverEnvironmentDoesNotInheritAuthorityOrSecrets(t *testing.T) {
	t.Parallel()
	got := driverEnvironment([]string{"HOME=/synthetic", "DISPLAY=:1", "OPENAI_API_KEY=secret", "DYLD_INSERT_LIBRARIES=evil", "CUA_DRIVER_PERMISSION_MODE=unrestricted", "CUA_DRIVER_CAPABILITY_MANIFEST=/evil", "PATH=/usr/bin"})
	text := strings.Join(got, "\n")
	for _, forbidden := range []string{"secret", "evil", "unrestricted"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("unsafe child environment")
		}
	}
	if !strings.Contains(text, "DISPLAY=:1") || !strings.Contains(text, "CUA_DRIVER_PERMISSION_MODE=standard") || !strings.Contains(text, "CUA_DRIVER_RS_UPDATE_CHECK=false") {
		t.Fatal("missing required environment")
	}
}
