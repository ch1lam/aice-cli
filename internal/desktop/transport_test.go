package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixturePeer struct {
	calls       atomic.Int32
	initializes atomic.Int32
	pages       atomic.Int32
}

// A raw fake peer tests the actual legacy wire contract, independently of the
// SDK server implementation. It never starts Cua or reads desktop content.
func fakeTransport(t *testing.T, mode string) (*mcp.IOTransport, *fixturePeer) {
	t.Helper()
	schemas := schemaFixture(t)
	schemas["unreviewed_tool"] = json.RawMessage(`{"type":"object"}`)
	if mode == "missing-tool" {
		delete(schemas, "drag")
	}
	if mode == "changed-schema" {
		schemas["check_permissions"] = json.RawMessage(`{"type":"object"}`)
	}
	allNames := slices.Sorted(maps.Keys(schemas))
	local, peer := net.Pipe()
	state := &fixturePeer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer peer.Close()
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if decoder.Decode(&request) != nil {
				return
			}
			if len(request.ID) == 0 {
				continue
			}
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
					list = append(list, map[string]any{"name": name, "inputSchema": schemas[name]})
				}
				result = map[string]any{"tools": list, "nextCursor": next}
			case "tools/call":
				state.calls.Add(1)
				switch mode {
				case "eof":
					return
				case "cancel":
					// Keep reading so the SDK can send its cancellation notification.
					continue
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
			if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = local.Close(); _ = peer.Close(); <-done })
	return &mcp.IOTransport{Reader: &boundedReader{ReadCloser: local, limit: maxMessageBytes}, Writer: local}, state
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

func TestMessageSizeBound(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, input string
		limit       int
		fails       bool
	}{
		{"exact", "1234\n1234\n", 4, false}, {"over", "12345\n", 4, true},
		{"large", strings.Repeat("x", 100*1024) + "\n", 200 * 1024, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &boundedReader{ReadCloser: io.NopCloser(strings.NewReader(test.input)), limit: test.limit}
			_, err := io.Copy(io.Discard, reader)
			if (err != nil) != test.fails {
				t.Fatal(err)
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

func TestOwnedProcessClosesHungChild(t *testing.T) {
	if os.Getenv("AICE_DESKTOP_FAKE_CHILD") == "1" {
		// Block even after stdin closes. Parent must terminate only this process.
		reader := bufio.NewReader(os.Stdin)
		_, _ = reader.ReadString('\n')
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	t.Parallel()
	command := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessClosesHungChild$")
	command.Env = append(os.Environ(), "AICE_DESKTOP_FAKE_CHILD=1")
	transport := &processTransport{command: command}
	conn, err := transport.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = conn.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("child cleanup blocked")
	}
	if command.ProcessState == nil {
		t.Fatal("owned process was not waited")
	}
	_ = conn.Close()
}

func TestInitializeCancellationClosesBlockedWriter(t *testing.T) {
	t.Parallel()
	local, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	// Peer never reads initialize. The connection must close to unblock Write.
	done := make(chan error, 1)
	go func() { _, err := connect(ctx, &mcp.IOTransport{Reader: local, Writer: local}); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(3 * time.Second):
		_ = local.Close()
		<-done
		t.Fatal("initialize write ignored cancellation")
	}
}
