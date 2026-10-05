package mcpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestSDKContentMapping(t *testing.T) {
	for _, tc := range []struct {
		name, raw       string
		read            bool
		kind            BlockKind
		uri, text, data string
	}{
		{name: "text", raw: `{"type":"text","text":"hello"}`, kind: BlockText, text: "hello"},
		{name: "empty text", raw: `{"type":"text","text":""}`, kind: BlockText},
		{name: "link", raw: `{"type":"resource_link","uri":"fixture://one","name":"one","description":"untrusted"}`, kind: BlockResourceLink, uri: "fixture://one"},
		{name: "embedded text", raw: `{"type":"resource","resource":{"uri":"fixture://two","text":"embedded"}}`, kind: BlockResourceText, uri: "fixture://two", text: "embedded"},
		{name: "embedded blob", raw: `{"type":"resource","resource":{"uri":"fixture://three","blob":"YWJj","mimeType":"application/octet-stream"}}`, kind: BlockResourceBlob, uri: "fixture://three", data: "abc"},
		{name: "read", raw: `{"uri":"fixture://read","text":"explicit"}`, read: true, kind: BlockResourceText, uri: "fixture://read", text: "explicit"},
		{name: "read blob", raw: `{"uri":"fixture://read","blob":"YWJj"}`, read: true, kind: BlockResourceBlob, uri: "fixture://read", data: "abc"},
		{name: "empty blob", raw: `{"uri":"fixture://empty","blob":""}`, read: true, kind: BlockResourceBlob, uri: "fixture://empty"},
		{name: "ambiguous resource", raw: `{"type":"resource","resource":{"uri":"fixture://bad","text":"one","blob":"YWJj"}}`, kind: BlockUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := openFixture(t, &httpFixture{result: func(req fixtureRequest) string {
				if req.Method == "tools/call" {
					return `{"content":[` + tc.raw + `]}`
				}
				if req.Method == "resources/read" {
					return `{"contents":[` + tc.raw + `]}`
				}
				return fixtureResult(req)
			}}, nil)
			var result Result
			var err error
			if tc.read {
				result, err = c.ReadResource(t.Context(), "fixture://read")
			} else {
				result, err = c.Call(t.Context(), "echo", json.RawMessage(`{}`))
			}
			if err != nil || len(result.Content) != 1 || result.State != llm.ExecutionReturned {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			block := result.Content[0]
			if block.Kind != tc.kind || block.Resource.URI != tc.uri || block.Text != tc.text || string(block.Data) != tc.data {
				t.Fatalf("block=%+v", block)
			}
		})
	}
}

func TestSDKDecodeFailureIsNotRescuedOrReplayed(t *testing.T) {
	for _, sse := range []bool{false, true} {
		for _, raw := range []string{
			`{"type":"future","number":9007199254740993}`,
			`{"type":"image","data":"not base64","mimeType":"image/png"}`,
			`{"type":"text","text":42}`,
			`null`,
		} {
			t.Run(fmt.Sprintf("sse=%v/%s", sse, raw), func(t *testing.T) {
				f := &httpFixture{sse: sse, result: func(req fixtureRequest) string {
					if req.Method == "tools/call" {
						return `{"content":[{"type":"text","text":"before"},` + raw + `,{"type":"text","text":"after"}],"structuredContent":{"n":9007199254740993}}`
					}
					return fixtureResult(req)
				}}
				c := openFixture(t, f, nil)
				result, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`))
				if !errors.Is(err, ErrProtocol) || !result.IsError || result.State != llm.ExecutionReturned || result.Loss == "" ||
					len(result.Content) != 0 || len(result.StructuredContent) != 0 || f.calls.Load() != 1 {
					t.Fatalf("result=%+v err=%v calls=%d", result, err, f.calls.Load())
				}
			})
		}
	}
}

func TestSDKResourceDecodeFailure(t *testing.T) {
	c := openFixture(t, &httpFixture{result: func(req fixtureRequest) string {
		if req.Method == "resources/read" {
			return `{"contents":[{"uri":"fixture://one","text":"before"},{"uri":"fixture://bad","blob":"not base64"}]}`
		}
		return fixtureResult(req)
	}}, nil)
	result, err := c.ReadResource(t.Context(), "fixture://one")
	if !errors.Is(err, ErrProtocol) || !result.IsError || result.State != llm.ExecutionReturned || len(result.Content) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
