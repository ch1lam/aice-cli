package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func TestAdmittedCatalogRetainsMetadataAndOwnsValues(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "normal")
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	catalog, err := c.Tools(t.Context())
	if err != nil || !catalog.Complete || len(catalog.Items) != 15 {
		t.Fatal("incomplete admitted catalog", err)
	}
	for i, item := range catalog.Items {
		if item.Name == "unreviewed_tool" {
			t.Fatal("unknown upstream tool exposed")
		}
		if item.Description != "synthetic descriptor" || !bytes.Contains(item.OutputSchema, []byte("9007199254740993")) || len(item.Annotations) == 0 {
			t.Fatal("descriptor fields lost")
		}
		catalog.Items[i].Name = "changed"
		for _, raw := range []json.RawMessage{catalog.Items[i].InputSchema, catalog.Items[i].OutputSchema, catalog.Items[i].Annotations} {
			raw[0] = '!'
		}
	}
	again, err := c.Tools(t.Context())
	if err != nil || again.Generation != c.ToolGeneration() {
		t.Fatal("catalog generation changed", err)
	}
	for _, item := range again.Items {
		if item.Name == "changed" || !json.Valid(item.InputSchema) || !json.Valid(item.OutputSchema) || !json.Valid(item.Annotations) {
			t.Fatal("caller mutated admitted catalog")
		}
	}
	if peer.pages.Load() != 2 || peer.calls.Load() != 0 {
		t.Fatal("cached catalog performed native I/O")
	}
}

func TestReviewedCallChecksBeforeDispatchAndPreservesGenericResult(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "normal")
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	rejected := errors.New("application permit changed")
	result, err := c.CallChecked(t.Context(), "click", []byte(`{}`), func(context.Context) error { return rejected })
	if !errors.Is(err, rejected) || result.State != llm.ExecutionNotDispatched || peer.calls.Load() != 0 {
		t.Fatal("rejected permit dispatched", err)
	}
	result, err = c.CallChecked(t.Context(), "unreviewed_tool", []byte(`{}`), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || peer.calls.Load() != 0 {
		t.Fatal("unreviewed tool reached wire")
	}
	result, err = c.CallChecked(t.Context(), "click", []byte(`{}`), nil)
	if err != nil || result.State != llm.ExecutionReturned || !result.IsError || len(result.Content) != 2 || result.Content[0].Kind != "text" || result.Content[1].Kind != "image" || len(result.StructuredContent) == 0 || peer.calls.Load() != 1 {
		t.Fatal("generic result was changed", err)
	}
}

func TestDriverCatalogChangePreventsOldCallsAndRetiresManager(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "changed-after-call")
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	// First response is real and remains usable even though it invalidates the
	// catalog for subsequent dispatch. It must never be retried automatically.
	first, err := c.CallChecked(t.Context(), "click", []byte(`{}`), nil)
	if err != nil || first.State != llm.ExecutionReturned || !first.IsError {
		t.Fatal("returned result lost after notification", err)
	}
	if _, err := c.Tools(t.Context()); !errors.Is(err, errDriverCatalogChanged) {
		t.Fatal("stale catalog remained available", err)
	}
	second, err := c.CallChecked(t.Context(), "click", []byte(`{}`), nil)
	if !errors.Is(err, errDriverCatalogChanged) || second.State != llm.ExecutionNotDispatched || peer.calls.Load() != 1 {
		t.Fatal("old catalog dispatched", err)
	}
	// The lifecycle call path must retain pre-dispatch rejection while retiring
	// the connection and its native sessions.
	m := newManager(func(context.Context) (driverClient, error) { t.Fatal("implicit reconnect"); return nil, nil })
	defer m.Close()
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m.client = c
	m.status.Connected = true
	m.runs[r] = struct{}{}
	r.active, r.started = true, true
	reply, err := r.callLocked(t.Context(), "click", map[string]any{})
	action := actionResult(reply, err)
	if action.Dispatched || action.Outcome != "not_dispatched" || m.Status().Connected || r.started || peer.calls.Load() != 1 || peer.pages.Load() != 2 {
		t.Fatal("invalidated admission retained authority or replayed")
	}
}

func TestClosedAdmissionCannotServeCachedCatalog(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "normal")
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tools(t.Context()); !errors.Is(err, mcpclient.ErrClosed) {
		t.Fatal("closed admission served tools", err)
	}
	result, err := c.CallChecked(t.Context(), "click", []byte(`{}`), nil)
	if !errors.Is(err, mcpclient.ErrClosed) || result.State != llm.ExecutionNotDispatched || peer.calls.Load() != 0 {
		t.Fatal("closed admission dispatched", err)
	}
	reply, err := c.call(t.Context(), "click", map[string]any{})
	if got := actionResult(reply, err); got.Dispatched || got.Outcome != "not_dispatched" {
		t.Fatal("closed client became unknown execution")
	}
}

func TestReviewedCallRetainsOrderedAndUnsupportedBlocks(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "ordered-content")
	c, err := connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	result, err := c.CallChecked(t.Context(), "click", []byte(`{}`), nil)
	if err != nil || result.State != llm.ExecutionReturned || !result.IsError || len(result.Content) != 5 || !bytes.Contains(result.StructuredContent, []byte("9007199254740993")) {
		t.Fatal("raw result was discarded", err)
	}
	for i, want := range []mcpclient.BlockKind{mcpclient.BlockText, mcpclient.BlockImage, mcpclient.BlockText, mcpclient.BlockAudio, mcpclient.BlockUnsupported} {
		if result.Content[i].Kind != want {
			t.Fatal("content order changed", i)
		}
	}
	if !bytes.Equal(result.Content[1].Data, []byte{1, 2, 3}) || !bytes.Contains(result.Content[4].Unsupported, []byte("9007199254740993")) || peer.calls.Load() != 1 {
		t.Fatal("source payload or dispatch count changed")
	}
}
