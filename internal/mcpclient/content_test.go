package mcpclient

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestContentNormalizationPreservesUnsupportedAndResourceProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, raw       string
		read            bool
		kind            BlockKind
		uri, text, data string
	}{
		{name: "text", raw: `{"type":"text","text":"hello"}`, kind: BlockText, text: "hello"},
		{name: "link", raw: `{"type":"resource_link","uri":"fixture://one","name":"one","description":"untrusted"}`, kind: BlockResourceLink, uri: "fixture://one"},
		{name: "embedded text", raw: `{"type":"resource","resource":{"uri":"fixture://two","text":"embedded"}}`, kind: BlockResourceText, uri: "fixture://two", text: "embedded"},
		{name: "embedded blob", raw: `{"type":"resource","resource":{"uri":"fixture://three","blob":"YWJj","mimeType":"application/octet-stream"}}`, kind: BlockResourceBlob, uri: "fixture://three", data: "abc"},
		{name: "read", raw: `{"uri":"fixture://read","text":"explicit"}`, read: true, kind: BlockResourceText, uri: "fixture://read", text: "explicit"},
		{name: "bad base64", raw: `{"type":"image","data":"not base64","mimeType":"image/png"}`, kind: BlockUnsupported},
		{name: "missing text", raw: `{"type":"text"}`, kind: BlockUnsupported},
		{name: "ambiguous resource", raw: `{"type":"resource","resource":{"uri":"fixture://bad","text":"one","blob":"YWJj"}}`, kind: BlockUnsupported},
		{name: "future", raw: `{"type":"future","number":9007199254740993}`, kind: BlockUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := decodeBlock(json.RawMessage(tc.raw), tc.read)
			if block.Kind != tc.kind || block.Resource.URI != tc.uri || block.Text != tc.text || string(block.Data) != tc.data {
				t.Fatalf("block=%+v", block)
			}
			if tc.kind == BlockUnsupported && !bytes.Equal(block.Unsupported, []byte(tc.raw)) {
				t.Fatal("unsupported source changed")
			}
		})
	}
}
