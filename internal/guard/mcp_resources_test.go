package guard

import (
	"github.com/ch1lam/aice-cli/internal/llm"
	"testing"
)

func TestMCPResourcePermissionCannotCollideWithRemoteTool(t *testing.T) {
	gate, _ := New("", Config{})
	service := mcpPolicyFixture()
	service.Tools = []MCPToolPolicy{{Name: llm.OperationResourceRead, SchemaFingerprint: "same", Allowed: true}}
	publishMCPPolicy(t, gate, service)
	ordinary := mcpPolicyBinding(service, llm.OperationResourceRead)
	_, permit, _ := gate.CheckMCP(t.Context(), "ordinary", ordinary, service.PermissionScope)
	if err := permit.AllowSession(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	resource := ordinary
	resource.Operation = llm.OperationResourceRead
	resourcePolicy := service
	resourcePolicy.Tools = []MCPToolPolicy{{Operation: llm.OperationResourceRead, Name: resource.ToolName, SchemaFingerprint: resource.SchemaFingerprint, Allowed: true}}
	if err := gate.RefreshMCPResources(resourcePolicy); err != nil {
		t.Fatal(err)
	}
	check := func(binding llm.ToolBinding, want Decision) *MCPPermit {
		t.Helper()
		result, permit, err := gate.CheckMCP(t.Context(), "model-name", binding, service.PermissionScope)
		if err != nil || result.Decision != want {
			t.Fatalf("operation %q = %+v %v", binding.Operation, result, err)
		}
		return permit
	}
	check(ordinary, DecisionAllow)
	resourcePermit := check(resource, DecisionAsk)
	if err := resourcePermit.AllowSession(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := gate.RefreshMCPTools(service); err != nil {
		t.Fatal(err)
	}
	check(resource, DecisionAllow)
	service.Tools = nil
	if err := gate.RefreshMCPTools(service); err != nil {
		t.Fatal(err)
	}
	check(ordinary, DecisionDeny)
	check(resource, DecisionAllow)
	resourcePolicy.Tools = nil
	if err := gate.RefreshMCPResources(resourcePolicy); err != nil {
		t.Fatal(err)
	}
	if resourcePermit.Validate(t.Context()) == nil {
		t.Fatal("removed resource operation retained permission")
	}
	resourcePolicy.Tools = []MCPToolPolicy{{Operation: llm.OperationResourceRead, Name: resource.ToolName, SchemaFingerprint: resource.SchemaFingerprint, Allowed: true}}
	if err := gate.RefreshMCPResources(resourcePolicy); err != nil {
		t.Fatal(err)
	}
	check(resource, DecisionAsk)
	gate.RevokeMCPService(service.Source, service.ServiceID)
	if gate.RefreshMCPResources(resourcePolicy) == nil {
		t.Fatal("discovery undid revocation")
	}
	check(resource, DecisionDeny)
}
