package guard

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func mcpPolicyFixture() MCPService {
	return MCPService{
		Source: "user:/settings.json", ServiceID: "docs", ConnectionFingerprint: "endpoint-account-v1",
		PermissionScope: "configured-tools", Enabled: true,
		Tools: []MCPToolPolicy{{Name: "read", SchemaFingerprint: "schema-1", Allowed: true}, {Name: "write", SchemaFingerprint: "schema-2", Allowed: true}, {Name: "blocked", SchemaFingerprint: "schema-3", Allowed: false}},
	}
}

func mcpPolicyBinding(service MCPService, name string) llm.ToolBinding {
	binding := llm.ToolBinding{Source: service.Source, ServiceID: service.ServiceID, ConnectionFingerprint: service.ConnectionFingerprint, ToolName: name}
	for _, tool := range service.Tools {
		if tool.Name == name {
			binding.SchemaFingerprint = tool.SchemaFingerprint
		}
	}
	return binding
}

func checkMCPPolicy(t *testing.T, g *Guard, service MCPService, name string, want Decision) *MCPPermit {
	t.Helper()
	result, permit, err := g.CheckMCP(t.Context(), "mapped_"+name, mcpPolicyBinding(service, name), service.PermissionScope)
	if err != nil || result.Decision != want {
		t.Fatalf("CheckMCP(%s) = %+v, %v; want %s", name, result, err, want)
	}
	if want == DecisionDeny && permit != nil {
		t.Fatal("denial returned a permit")
	}
	return permit
}

func publishMCPPolicy(t *testing.T, g *Guard, service MCPService) {
	t.Helper()
	if err := g.SetMCPService(service); err != nil {
		t.Fatal(err)
	}
}

func TestMCPGrantsBindIdentityAndTool(t *testing.T) {
	t.Parallel()
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	g.AllowToolSession("mapped_read")
	permit := checkMCPPolicy(t, g, service, "read", DecisionAsk)
	if err := permit.AllowSession(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	checkMCPPolicy(t, g, service, "read", DecisionAllow)
	checkMCPPolicy(t, g, service, "write", DecisionAsk)
	checkMCPPolicy(t, g, service, "blocked", DecisionDeny)
	for _, change := range []func(*MCPService){
		func(s *MCPService) { s.Source = "project:/settings.json" },
		func(s *MCPService) { s.ServiceID = "other" },
	} {
		other := mcpPolicyFixture()
		change(&other)
		publishMCPPolicy(t, g, other)
		checkMCPPolicy(t, g, other, "read", DecisionAsk)
	}
	for _, field := range []string{"source", "service", "connection", "tool", "schema", "scope"} {
		t.Run(field, func(t *testing.T) {
			binding := mcpPolicyBinding(service, "read")
			scope := service.PermissionScope
			switch field {
			case "source":
				binding.Source = "spoofed"
			case "service":
				binding.ServiceID = "spoofed"
			case "connection":
				binding.ConnectionFingerprint = "spoofed"
			case "tool":
				binding.ToolName = "spoofed"
			case "schema":
				binding.SchemaFingerprint = "spoofed"
			case "scope":
				scope = "spoofed"
			}
			result, _, err := g.CheckMCP(t.Context(), "mapped_read", binding, scope)
			if err != nil || result.Decision != DecisionDeny {
				t.Fatalf("forged identity allowed: %+v, %v", result, err)
			}
		})
	}
}

func TestMCPServiceGrantFreezesVersions(t *testing.T) {
	t.Parallel()
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	permit := checkMCPPolicy(t, g, service, "read", DecisionAsk)
	if err := permit.AllowSession(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	checkMCPPolicy(t, g, service, "read", DecisionAllow)
	checkMCPPolicy(t, g, service, "write", DecisionAllow)
	checkMCPPolicy(t, g, service, "blocked", DecisionDeny)
	// Merely refreshing the same catalog preserves authority.
	publishMCPPolicy(t, g, service)
	checkMCPPolicy(t, g, service, "write", DecisionAllow)
	service.Tools[0].SchemaFingerprint = "changed"
	service.Tools = append(service.Tools, MCPToolPolicy{Name: "new-tool", SchemaFingerprint: "schema-new", Allowed: true})
	publishMCPPolicy(t, g, service)
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
	checkMCPPolicy(t, g, service, "new-tool", DecisionAsk)
	checkMCPPolicy(t, g, service, "write", DecisionAllow)
	if err := permit.Validate(t.Context()); err == nil {
		t.Fatal("old schema permit survived")
	}
	// Reverting a schema cannot resurrect its old grant.
	service.Tools[0].SchemaFingerprint = "schema-1"
	publishMCPPolicy(t, g, service)
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
	if err := permit.AllowSession(t.Context(), false); err == nil {
		t.Fatal("old approval became valid after schema reversion")
	}
}

func TestMCPInvalidationCannotRevivePendingApprovals(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"connection", "scope", "disable", "filter", "remove-tool", "remove-service", "revoke", "new-session"} {
		t.Run(kind, func(t *testing.T) {
			g, _ := New("", Config{})
			original := mcpPolicyFixture()
			service := mcpPolicyFixture()
			publishMCPPolicy(t, g, service)
			permit := checkMCPPolicy(t, g, service, "read", DecisionAsk)
			if err := permit.AllowSession(t.Context(), false); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "connection":
				service.ConnectionFingerprint = "rotated-credential"
			case "scope":
				service.PermissionScope = "different-control-mode"
			case "disable":
				service.Enabled = false
			case "filter":
				service.Tools[0].Allowed = false
			case "remove-tool":
				service.Tools = service.Tools[1:]
			case "remove-service":
				g.RemoveMCPService(service.Source, service.ServiceID)
			case "revoke":
				g.RevokeMCPService(service.Source, service.ServiceID)
			case "new-session":
				g.ResetSessionGrants()
			}
			if kind != "remove-service" {
				publishMCPPolicy(t, g, service)
			}
			if permit.Validate(t.Context()) == nil || permit.AllowSession(t.Context(), false) == nil {
				t.Fatal("invalidated permit remained usable")
			}
			if kind == "revoke" {
				checkMCPPolicy(t, g, original, "read", DecisionDeny)
				g.RestoreMCPService(service.Source, service.ServiceID)
			}
			publishMCPPolicy(t, g, original)
			checkMCPPolicy(t, g, original, "read", DecisionAsk)
			if permit.Validate(t.Context()) == nil || permit.AllowSession(t.Context(), true) == nil {
				t.Fatal("restoring configuration revived stale approval")
			}
		})
	}
}

func TestMCPCatalogChangeDuringServiceApprovalIsAtomic(t *testing.T) {
	t.Parallel()
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	permit := checkMCPPolicy(t, g, service, "read", DecisionAsk)
	service.Tools[1].SchemaFingerprint = "new-write"
	publishMCPPolicy(t, g, service)
	if err := permit.AllowSession(t.Context(), true); err == nil {
		t.Fatal("changed service catalog granted")
	}
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
	checkMCPPolicy(t, g, service, "write", DecisionAsk)
	// A tool-only grant does not accidentally depend on unrelated schemas.
	if err := permit.AllowSession(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	checkMCPPolicy(t, g, service, "read", DecisionAllow)
}

func TestMCPDisabledLegacyGuardStillChecksAuthority(t *testing.T) {
	t.Parallel()
	disabled := false
	g, _ := New("", Config{Enabled: &disabled})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
	checkMCPPolicy(t, g, service, "blocked", DecisionDeny)
	g.RevokeMCPService(service.Source, service.ServiceID)
	checkMCPPolicy(t, g, service, "read", DecisionDeny)
}

func TestMCPCancellationAndInvalidPolicy(t *testing.T) {
	t.Parallel()
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	permit := checkMCPPolicy(t, g, service, "read", DecisionAsk)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := g.CheckMCP(ctx, "mapped_read", mcpPolicyBinding(service, "read"), service.PermissionScope); err == nil {
		t.Fatal("canceled check succeeded")
	}
	if permit.AllowSession(ctx, true) == nil || permit.Validate(ctx) == nil {
		t.Fatal("canceled permission succeeded")
	}
	for _, input := range []MCPService{
		{}, {Source: "bad\x1b[0m"},
		{Source: "s", ServiceID: "i", ConnectionFingerprint: "c", PermissionScope: "p", Tools: []MCPToolPolicy{{Name: "a", SchemaFingerprint: "s", Allowed: true}, {Name: "a", SchemaFingerprint: "s", Allowed: true}}},
	} {
		if err := g.SetMCPService(input); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
}

func TestMCPConcurrentGrantRevokeAndCatalogUpdates(t *testing.T) {
	t.Parallel()
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	publishMCPPolicy(t, g, service)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Go(func() {
			for range 50 {
				switch worker {
				case 0:
					_, permit, _ := g.CheckMCP(t.Context(), "mapped_read", mcpPolicyBinding(service, "read"), service.PermissionScope)
					if permit != nil {
						_ = permit.AllowSession(t.Context(), true)
						_ = permit.Validate(t.Context())
					}
				case 1:
					_ = g.SetMCPService(service)
				case 2:
					g.RevokeMCPService(service.Source, service.ServiceID)
					g.RestoreMCPService(service.Source, service.ServiceID)
				case 3:
					g.ResetSessionGrants()
				}
			}
		})
	}
	workers.Wait()
	g.RevokeMCPService(service.Source, service.ServiceID)
	checkMCPPolicy(t, g, service, "read", DecisionDeny)
	// Bounded service storage does not evict existing denials or authority.
	for i := range 128 {
		other := mcpPolicyFixture()
		other.ServiceID = fmt.Sprint(i)
		publishMCPPolicy(t, g, other)
	}
	service.ServiceID = "overflow"
	if g.SetMCPService(service) == nil {
		t.Fatal("unbounded service storage")
	}
}
