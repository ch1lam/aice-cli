package mcpclient

import (
	"bytes"
	"encoding/json"
	"sync"
	"unicode/utf8"
)

// The pinned SDK decodes arbitrary JSON through float64. Observe bounded wire
// frames before it does so, retaining exact schema/structured-result JSON. The
// SDK still owns handshake, IDs, negotiation, notifications and cancellation.
// Operations are serialized per connection; no unbounded response map exists.
type receipts struct {
	mu             sync.Mutex
	current        *receipt
	onNotification func(string)
}

type receipt struct {
	method     string
	id         string
	attempted  bool
	result     json.RawMessage
	rpcError   bool
	limited    bool
	httpStatus int
}

func (r *receipts) begin(method string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = &receipt{method: method}
}

func (r *receipts) finish() receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	value := *r.current
	r.current = nil
	return value
}

func (r *receipts) limit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		r.current.limited = true
	}
}

func (r *receipts) observe(data []byte, outgoing bool) {
	if !utf8.Valid(data) {
		return
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &msg) != nil || msg.JSONRPC != "2.0" || (len(msg.Result) > 0 && len(msg.Error) > 0) {
		return // malformed frames are rejected by the pinned protocol SDK
	}
	id := string(bytes.TrimSpace(msg.ID))
	if !outgoing && id == "" && r.onNotification != nil {
		r.onNotification(msg.Method)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.current
	if c == nil || id == "" || id == "null" {
		return
	}
	if outgoing && msg.Method == c.method && c.id == "" {
		c.id, c.attempted = id, true // before write: a failed write may be partial
	}
	if !outgoing && msg.Method == "" && c.id == id && len(c.result) == 0 && !c.rpcError {
		c.result = msg.Result
		c.rpcError = len(msg.Error) > 0
	}
}

func (r *receipts) responseStatus(request []byte, status int) {
	if status < 300 {
		return
	}
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(request, &msg) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil && r.current.id != "" && r.current.id == string(bytes.TrimSpace(msg.ID)) {
		r.current.httpStatus = status
	}
}
