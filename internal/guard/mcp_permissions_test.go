package guard

import "testing"

func TestMCPUserRulesSurviveSessionResetButNotPolicyChanges(t *testing.T) {
	g, _ := New("", Config{})
	service := mcpPolicyFixture()
	service.Tools[0].UserDecision = DecisionAllow
	service.Tools[1].UserDecision = DecisionDeny
	service.Tools[2].UserDecision = DecisionAllow // cannot bypass configured filter
	publishMCPPolicy(t, g, service)
	permit := checkMCPPolicy(t, g, service, "read", DecisionAllow)
	checkMCPPolicy(t, g, service, "write", DecisionDeny)
	checkMCPPolicy(t, g, service, "blocked", DecisionDeny)
	g.ResetSessionGrants()
	if permit.Validate(t.Context()) == nil {
		t.Fatal("in-flight permit crossed Session reset")
	}
	permit = checkMCPPolicy(t, g, service, "read", DecisionAllow)
	service.Tools[0].UserDecision = DecisionAsk
	publishMCPPolicy(t, g, service)
	if permit.Validate(t.Context()) == nil {
		t.Fatal("removed rule retained in-flight permit")
	}
	checkMCPPolicy(t, g, service, "read", DecisionAsk)
	service.Tools[0].UserDecision = DecisionDeny
	publishMCPPolicy(t, g, service)
	checkMCPPolicy(t, g, service, "read", DecisionDeny)
}
