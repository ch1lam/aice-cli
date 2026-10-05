package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type mcpBackendFunc func(context.Context, string, json.RawMessage) (mcpclient.Result, error)

func (f mcpBackendFunc) Call(ctx context.Context, name string, args json.RawMessage) (mcpclient.Result, error) {
	return f(ctx, name, args)
}

func mcpTestOptions(backend MCPBackend) MCPOptions {
	return MCPOptions{Definition: llm.ToolDefinition{Name: "mapped", Description: "description", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Binding: llm.ToolBinding{Source: "user", ServiceID: "fixture", ConnectionFingerprint: "connection", ToolName: "original", SchemaFingerprint: "schema"}, Backend: backend}
}

func TestMCPOrderedPartialResultsAndOriginal(t *testing.T) {
	t.Parallel()
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2100, 10))); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"integer":9007199254740993,"decimal":1.234567890123456789}`)
	calls := 0
	m, err := NewMCP(mcpTestOptions(mcpBackendFunc(func(_ context.Context, name string, args json.RawMessage) (mcpclient.Result, error) {
		calls++
		if name != "original" || string(args) != `{"n":9007199254740993}` {
			t.Errorf("call remapped incorrectly: %s %s", name, args)
		}
		return mcpclient.Result{State: llm.ExecutionReturned, IsError: true, StructuredContent: raw, Content: []mcpclient.Block{
			{Kind: mcpclient.BlockText, Text: "before"}, {Kind: mcpclient.BlockImage, Data: pngBytes.Bytes(), MIMEType: "image/png"},
			{Kind: mcpclient.BlockText, Text: "after"}, {Kind: mcpclient.BlockResourceLink, Resource: mcpclient.Resource{URI: "fixture://r", Name: "r"}},
			{Kind: mcpclient.BlockResourceText, Resource: mcpclient.Resource{URI: "fixture://text", MIMEType: "text/plain"}, Text: "embedded"},
		}}, errors.New("private backend diagnostic")
	})))
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Execute(t.Context(), llm.ToolCall{ID: "c", Name: "mapped", Arguments: json.RawMessage(`{"n":9007199254740993}`)})
	if err != nil || calls != 1 || !result.IsError || result.Details.State != llm.ExecutionReturned || !bytes.Equal(result.Details.StructuredContent, raw) {
		t.Fatalf("result: %+v %v calls=%d", result, err, calls)
	}
	if result.Content[0].Text != "before" || result.Content[1].Image == nil || result.Content[2].Text != "after" || !strings.Contains(result.Content[3].Text, "not fetched") || !strings.Contains(result.Content[4].Text, "embedded") {
		t.Fatal("content reordered")
	}
	img := result.Content[1].Image
	if img.Width != 2000 || img.Original == nil || !bytes.Equal(img.Original.Data, pngBytes.Bytes()) {
		t.Fatal("lost source image or coordinates")
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("private backend")) {
		t.Fatal("raw backend error leaked")
	}
	if err := result.Details.Validate(); err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	pngBytes.Bytes()[0] = 0
	if result.Details.StructuredContent[0] != '{' || img.Original.Data[0] != 0x89 {
		t.Fatal("result aliases backend memory")
	}
}

func TestMCPRejectsInvalidCallsBeforeDispatch(t *testing.T) {
	t.Parallel()
	calls := 0
	m, _ := NewMCP(mcpTestOptions(mcpBackendFunc(func(context.Context, string, json.RawMessage) (mcpclient.Result, error) {
		calls++
		return mcpclient.Result{}, nil
	})))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		ctx        context.Context
		name, args string
	}{{t.Context(), "wrong", `{}`}, {ctx, "mapped", `{}`}, {t.Context(), "mapped", `null`}, {t.Context(), "mapped", `[]`}, {t.Context(), "mapped", `{"x":`}, {t.Context(), "mapped", strings.Repeat(" ", 1<<20) + `{}`}} {
		// Oversized whitespace is rejected by the transport too; use a large object here.
		if len(test.args) > 1<<20 {
			test.args = `{"x":"` + strings.Repeat("a", 1<<20) + `"}`
		}
		result, err := m.Execute(test.ctx, llm.ToolCall{Name: test.name, Arguments: []byte(test.args)})
		if err != nil || !result.IsError || result.Details.State != llm.ExecutionNotDispatched {
			t.Fatalf("invalid call accepted: %+v %v", result, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid calls dispatched")
	}
}

func TestMCPRedactsKnownCredentialsInAllTextualMetadata(t *testing.T) {
	t.Parallel()
	secret := `secret"value`
	options := mcpTestOptions(mcpBackendFunc(func(context.Context, string, json.RawMessage) (mcpclient.Result, error) {
		return mcpclient.Result{State: llm.ExecutionReturned, StructuredContent: json.RawMessage(`{"credential":"secret\"value","n":9007199254740993}`), Content: []mcpclient.Block{
			{Kind: mcpclient.BlockText, Text: secret}, {Kind: mcpclient.BlockResourceLink, Resource: mcpclient.Resource{URI: secret, Name: secret, Description: secret}},
			{Kind: mcpclient.BlockResourceText, Resource: mcpclient.Resource{URI: secret}, Text: secret},
		}}, nil
	}))
	options.Secrets = []string{secret}
	m, err := NewMCP(options)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := m.Execute(t.Context(), llm.ToolCall{Name: "mapped", Arguments: []byte(`{}`)})
	encoded, _ := json.Marshal(result)
	var decoded any
	_ = json.Unmarshal(encoded, &decoded)
	if strings.Contains(string(encoded), "secret") || strings.Contains(result.Details.Loss, secret) || !bytes.Contains(result.Details.StructuredContent, []byte("9007199254740993")) {
		t.Fatalf("redaction failed: %s", encoded)
	}
	// Earlier duplicate fields must be examined before map decoding overwrites them.
	raw, changed, err := newMCPRedactor([]string{"token"}).json([]byte(`{"a":"\u0074oken","a":"public"}`))
	if err != nil || !changed || bytes.Contains(raw, []byte("0074")) {
		t.Fatalf("duplicate-field leak: %s %v %v", raw, changed, err)
	}
	options.Definition.InputSchema = []byte(`{"type":"object","description":"secret\"value"}`)
	if _, err := NewMCP(options); err == nil {
		t.Fatal("credential-bearing schema admitted")
	}
}

func TestMCPStorageLossIsExplicit(t *testing.T) {
	t.Parallel()
	options := mcpTestOptions(mcpBackendFunc(func(context.Context, string, json.RawMessage) (mcpclient.Result, error) {
		return mcpclient.Result{State: llm.ExecutionUnknown, StructuredContent: json.RawMessage(`{bad`), Content: []mcpclient.Block{
			{Kind: mcpclient.BlockText, Text: strings.Repeat("中", maxMCPTextBytes)},
			{Kind: mcpclient.BlockImage, MIMEType: "image/png", Data: []byte("invalid")},
			{Kind: mcpclient.BlockAudio, Data: []byte("audio")}, {Kind: mcpclient.BlockUnsupported},
		}}, errors.New("lost response")
	}))
	m, _ := NewMCP(options)
	result, _ := m.Execute(t.Context(), llm.ToolCall{Name: "mapped", Arguments: []byte(`{}`)})
	if !result.IsError || result.Details.State != llm.ExecutionUnknown || result.Details.Loss == "" || len(result.Content[0].Text) > maxMCPTextBytes || len(result.Details.StructuredContent) != 0 {
		t.Fatalf("loss not recorded: %+v", result.Details)
	}
	if err := result.Details.Validate(); err != nil {
		t.Fatal(err)
	}
}
