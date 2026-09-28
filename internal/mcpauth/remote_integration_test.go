//go:build integration

package mcpauth

import (
	"context"
	"testing"
	"time"
)

// Public metadata only: no registration, consent, tokens, model, or MCP call.
func TestLinearPublicOAuthDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c := Client{}
	in, err := c.Probe(ctx, "https://mcp.linear.app/mcp")
	if err != nil {
		t.Fatal(err)
	}
	in.Scopes = []string{"read"}
	s, err := c.Discover(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Resource() != "https://mcp.linear.app/mcp" || s.Issuer() != "https://mcp.linear.app" {
		t.Fatal("unexpected resource/issuer")
	}
	if _, err := s.PreRegistered("discovery-check-only", "", "none", "http://127.0.0.1:43123/callback"); err != nil {
		t.Fatal(err)
	}
	if s.registration == "" {
		t.Fatal("dynamic registration unavailable")
	}
	t.Logf("resource=%s issuer=%s token_endpoint=%s; public S256 authorization-code metadata validated", s.Resource(), s.Issuer(), s.TokenEndpoint())
}
