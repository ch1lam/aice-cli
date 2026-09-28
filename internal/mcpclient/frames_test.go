package mcpclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameBoundBeforeDecode(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		sse, body   bool
	}{
		{"stdio", strings.Repeat("x", 9000) + "\n", false, false},
		{"json", strings.Repeat("x", 9000), false, true},
		{"sse-line", "data: " + strings.Repeat("x", 9000) + "\n\n", true, false},
		{"sse-event", strings.Repeat("data: 12345678\n", 900) + "\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &receipts{}
			r.begin("tools/call")
			reader := newFrameReader(io.NopCloser(strings.NewReader(tc.input)), 8000, tc.sse, tc.body, r)
			data, err := io.ReadAll(reader)
			if !errors.Is(err, ErrLimit) || len(data) != 0 || !r.finish().limited {
				t.Fatalf("bytes=%d err=%v", len(data), err)
			}
		})
	}
}

func TestSSEExactMultilineReceiptAndNotification(t *testing.T) {
	changed := 0
	r := &receipts{onNotification: func(method string) {
		if method == "notifications/tools/list_changed" {
			changed++
		}
	}}
	r.begin("tools/call")
	r.observe([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call"}`), true)
	input := ": heartbeat\r\n\r\n" +
		"data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n" +
		"event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\r\ndata: \"result\":{\"n\":9007199254740993}}\r\n\r\n"
	data, err := io.ReadAll(newFrameReader(io.NopCloser(strings.NewReader(input)), 4096, true, false, r))
	received := r.finish()
	if err != nil || string(data) != input || changed != 1 || !bytes.Equal(received.result, json.RawMessage(`{"n":9007199254740993}`)) {
		t.Fatalf("%v changed=%d result=%s", err, changed, received.result)
	}
}

func TestReceiptDoesNotAcceptOtherIDsOrLateResults(t *testing.T) {
	r := &receipts{}
	r.begin("tools/call")
	r.observe([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`), true)
	r.observe([]byte(`{"jsonrpc":"2.0","id":2,"result":{"wrong":true}}`), false)
	if got := r.finish(); len(got.result) != 0 || !got.attempted {
		t.Fatalf("%+v", got)
	}
	r.begin("tools/call")
	r.observe([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call"}`), true)
	r.observe([]byte(`{"jsonrpc":"2.0","id":1,"result":{"late":true}}`), false)
	if got := r.finish(); len(got.result) != 0 {
		t.Fatalf("%+v", got)
	}
}
