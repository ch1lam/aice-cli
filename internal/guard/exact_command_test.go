package guard

import "testing"

func TestGuardExactCommandGrant(t *testing.T) {
	t.Parallel()

	g, err := New(t.TempDir(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	const granted = "rm -rf ./scratch"
	before, err := g.Check(t.Context(), toolCall("bash", map[string]any{"command": granted}))
	if err != nil || before.Decision != DecisionAsk {
		t.Fatalf("before grant = %#v, error = %v, want ask", before, err)
	}
	g.AllowCommandSession(granted)
	g.AllowCommandSession(granted)

	tests := []struct {
		name    string
		command string
		want    Decision
	}{
		{"identical", granted, DecisionAllow},
		{"path suffix", granted + "-other", DecisionAsk},
		{"extra argument", granted + " ./other", DecisionAsk},
		{"changed argument", "rm -rf ./other", DecisionAsk},
		{"compound suffix", granted + "; rm -rf ./other", DecisionAsk},
		{"compound prefix", "rm -rf ./other && " + granted, DecisionAsk},
		{"pipeline", granted + " | cat", DecisionAsk},
		{"leading whitespace", " " + granted, DecisionAsk},
		{"trailing whitespace", granted + " ", DecisionAsk},
		{"quoting differs", "rm -rf './scratch'", DecisionAsk},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := g.Check(t.Context(), toolCall("bash", map[string]any{"command": test.command}))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.want {
				t.Fatalf("Check(%q) = %#v, want %s", test.command, result, test.want)
			}
			if test.want == DecisionAsk && result.RuleID != "permissionGate.dangerous" {
				t.Fatalf("Check(%q) rule = %q, want dangerous-command check", test.command, result.RuleID)
			}
		})
	}
}

func TestGuardExactCommandGrantPreservesOtherChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		grant   string
		command string
		want    Decision
		rule    string
	}{
		{
			name: "configured substring still allows suffix",
			config: Config{PermissionGate: PermissionGateConfig{
				AllowedPatterns: []PatternConfig{{Pattern: "rm -rf ./configured"}},
			}},
			grant: "rm -rf ./scratch", command: "rm -rf ./configured-other",
			want: DecisionAllow,
		},
		{
			name: "configured regex still matches",
			config: Config{PermissionGate: PermissionGateConfig{
				AllowedPatterns: []PatternConfig{{Pattern: `^rm -rf ./configured-[0-9]+$`, Regex: true}},
			}},
			grant: "rm -rf ./scratch", command: "rm -rf ./configured-12",
			want: DecisionAllow,
		},
		{
			name: "auto deny overrides exact and configured allows",
			config: Config{PermissionGate: PermissionGateConfig{
				AllowedPatterns:  []PatternConfig{{Pattern: "rm -rf"}},
				AutoDenyPatterns: []PatternConfig{{Pattern: "./scratch"}},
			}},
			grant: "rm -rf ./scratch", command: "rm -rf ./scratch",
			want: DecisionDeny, rule: "permissionGate.autoDeny",
		},
		{
			name:  "file policy still denies",
			grant: "sudo cat .env", command: "sudo cat .env",
			want: DecisionDeny, rule: "secret-files",
		},
		{
			name:  "outside path still asks",
			grant: "rm -rf ../outside", command: "rm -rf ../outside",
			want: DecisionAsk, rule: "pathAccess.ask",
		},
		{
			name:  "empty grant does not allow commands",
			grant: "", command: "rm -rf ./scratch",
			want: DecisionAsk, rule: "permissionGate.dangerous",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g, err := NewWithExists(t.TempDir(), test.config, alwaysExists)
			if err != nil {
				t.Fatal(err)
			}
			g.AllowCommandSession(test.grant)
			result, err := g.Check(t.Context(), toolCall("bash", map[string]any{"command": test.command}))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.want || result.RuleID != test.rule {
				t.Fatalf("Check(%q) = %#v, want %s with rule %q", test.command, result, test.want, test.rule)
			}
		})
	}
}
