package mcpclient

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type httpTransport struct {
	base          *http.Transport
	lifetime      context.Context
	headers       http.Header
	authorization func() string
	limit         int
	receipts      *receipts
}

func openHTTP(config HTTPConfig, lifetime context.Context, limit int, receipts *receipts) (mcp.Transport, *http.Transport, error) {
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, nil, ErrConfig
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, nil, ErrConfig
	}
	headers := make(http.Header)
	for key, value := range config.Headers {
		key = http.CanonicalHeaderKey(key)
		if key == "" || strings.ContainsAny(key+value, "\r\n\x00") {
			return nil, nil, ErrConfig
		}
		switch key {
		case "Idempotency-Key", "X-Idempotency-Key", "Host", "Content-Length", "Transfer-Encoding", "Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version":
			return nil, nil, ErrConfig
		}
		if _, exists := headers[key]; exists {
			return nil, nil, ErrConfig
		}
		headers.Set(key, value)
	}
	if _, exists := headers["Authorization"]; config.Authorization != nil && exists {
		return nil, nil, ErrConfig
	}
	base := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout: 60 * time.Second, MaxIdleConns: 2, MaxIdleConnsPerHost: 2,
		MaxResponseHeaderBytes: 64 << 10,
	}
	client := &http.Client{
		Transport:     &httpTransport{base: base, lifetime: lifetime, headers: headers, authorization: config.Authorization, limit: limit, receipts: receipts},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// No OAuthHandler: the SDK can replay a POST after it authorizes a 401/403.
	// Authentication is coordinated before dispatch by the application instead.
	return &mcp.StreamableClientTransport{Endpoint: config.Endpoint, HTTPClient: client, MaxRetries: -1}, base, nil
}

func (t *httpTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.lifetime, cancel)
	if request.Method == http.MethodDelete {
		// SDK Close uses its detached session context and does not close this
		// response body. Bound cleanup and release its body here.
		stop()
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	}
	req := request.Clone(ctx)
	for key, values := range t.headers {
		req.Header[key] = append([]string(nil), values...)
	}
	if t.authorization != nil {
		value := t.authorization()
		if value == "" || len(value) > 16<<10 || strings.ContainsAny(value, "\r\n\x00") {
			stop()
			cancel()
			return nil, ErrConfig
		}
		req.Header.Set("Authorization", value)
	}
	var outgoing []byte
	if req.Method == http.MethodPost && request.GetBody != nil {
		body, err := request.GetBody()
		if err != nil {
			stop()
			cancel()
			return nil, ErrTransport
		}
		// JSON escaping can expand a valid 1 MiB argument object severalfold.
		// Observe the complete serialized envelope before allowing any write.
		data, err := io.ReadAll(io.LimitReader(body, maxMessageBytes+1))
		_ = body.Close()
		if err != nil {
			stop()
			cancel()
			return nil, ErrTransport
		}
		if len(data) > maxMessageBytes {
			stop()
			cancel()
			return nil, ErrLimit
		}
		t.receipts.observe(data, true)
		outgoing = data
	}
	// Disable even net/http's body replay on a reused connection. Never attach
	// idempotency headers that could make a tool POST eligible for retries.
	req.GetBody = nil
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		stop()
		cancel()
		return nil, err // Open/operation replaces this before exposing it
	}
	if req.Method == http.MethodDelete {
		_ = resp.Body.Close()
		resp.Body = http.NoBody
		cancel()
		return resp, nil
	}
	t.receipts.responseStatus(outgoing, resp.StatusCode)
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	observer := t.receipts
	if resp.StatusCode != http.StatusOK {
		observer = &receipts{} // HTTP error bodies are not MCP results
	}
	resp.Body = &cancelBody{
		ReadCloser: newFrameReader(resp.Body, t.limit, mediaType == "text/event-stream", mediaType != "text/event-stream", observer),
		cancel:     cancel, stop: stop,
	}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	stop   func() bool
}

func (b *cancelBody) Close() error {
	b.stop()
	b.cancel()
	return b.ReadCloser.Close()
}
