package mcpclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCatalogBoundsAndInvalidPages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limits    Limits
		page      string
		wantCount int
		wantError bool
	}{
		{name: "item", limits: Limits{CatalogItems: 1}, wantCount: 1},
		{name: "page", limits: Limits{Pages: 1}, wantCount: 1},
		{name: "bytes", limits: Limits{CatalogBytes: 100}},
		{name: "duplicate", page: `{"tools":[{"name":"same","inputSchema":{}},{"name":"same","inputSchema":{}}]}`, wantCount: 1, wantError: true},
		{name: "invalid schema", page: `{"tools":[{"name":"bad","inputSchema":[]}]}`, wantError: true},
		{name: "null", page: `{"tools":null}`, wantError: true},
		{name: "repeated cursor", page: `{"tools":[],"nextCursor":"loop"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &httpFixture{result: func(req fixtureRequest) string {
				if req.Method == "tools/list" && tc.page != "" {
					return tc.page
				}
				return fixtureResult(req)
			}}
			c := openFixture(t, f, func(c *Config) { c.Limits = tc.limits })
			catalog, err := c.Tools(t.Context())
			if (err != nil) != tc.wantError || catalog.Complete || catalog.Notice == "" || len(catalog.Items) != tc.wantCount {
				t.Fatalf("catalog=%+v error=%v", catalog, err)
			}
		})
	}
}

func TestNoSharedMutableCatalog(t *testing.T) {
	c := openFixture(t, &httpFixture{}, nil)
	a, err := c.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a.Items[0].InputSchema[0] = '!'
	b, err := c.Tools(t.Context())
	if err != nil || !json.Valid(b.Items[0].InputSchema) {
		t.Fatalf("aliased catalog %v", err)
	}
}

func TestUnsupportedCapabilitiesAndInvalidInitialization(t *testing.T) {
	f := &httpFixture{result: func(req fixtureRequest) string {
		if req.Method == "initialize" {
			return strings.Replace(fixtureInit, `"tools":{"listChanged":true},"resources":{"listChanged":true},"prompts":{}`, `"prompts":{}`, 1)
		}
		return fixtureResult(req)
	}}
	c := openFixture(t, f, nil)
	if _, err := c.Tools(t.Context()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := c.Resources(t.Context()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := c.Call(t.Context(), "echo", json.RawMessage(`{}`)); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("unsupported call dispatched")
	}
}
