package guard

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRule(t *testing.T) {
	workspace := t.TempDir()
	for _, text := range []string{"bash(go test *)", "bash(git push)", "read(~/go/pkg/mod/**)", "write(/tmp/x.txt)", "read(./docs/*.md)"} {
		if _, err := parseRule(text, workspace); err != nil {
			t.Errorf("parseRule(%q): %v", text, err)
		}
	}
	for _, text := range []string{"", "bash", "bash()", "bash(*)", "net(github.com)", "read(/**)", "write(~/**)", "read(x"} {
		if _, err := parseRule(text, workspace); err == nil {
			t.Errorf("parseRule(%q) accepted", text)
		}
	}
}

func TestGuardRulesPrecedence(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	g, err := New(workspace, Config{Rules: Rules{
		Allow: []string{"bash(git push *)", "read(" + filepath.ToSlash(outside) + "/**)", "write(" + filepath.ToSlash(outside) + "/out/**)"},
		Ask:   []string{"bash(git commit *)", "write(./docs/**)", "bash(git push --force *)"},
		Deny:  []string{"bash(curl *)", "read(./private/**)", "bash(sudo *)"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tool, arg string
		want      Decision
		rule      string
	}{
		{"bash", "git push origin main", DecisionAllow, ""},
		{"bash", "git -C . push", DecisionAllow, ""},
		{"bash", "git push --force", DecisionAsk, RuleUserAsk},
		{"bash", "git push && npm publish", DecisionAsk, RuleBashOutbound},
		{"bash", "git commit -m x", DecisionAsk, RuleUserAsk},
		{"bash", "git status", DecisionAllow, ""},
		{"bash", "curl -s https://example.com", DecisionDeny, RuleUserDeny},
		{"bash", "timeout 5 curl https://example.com", DecisionDeny, RuleUserDeny},
		{"bash", "sudo -u x ls", DecisionDeny, RuleUserDeny},
		{"bash", "cat " + filepath.ToSlash(outside) + "/a.txt", DecisionAllow, ""},
		{"bash", "echo x > " + filepath.ToSlash(outside) + "/a.txt", DecisionAsk, RuleOutsideWrite},
		{"bash", "echo x > " + filepath.ToSlash(outside) + "/out/a.txt", DecisionAllow, ""},
		{"read", "private/notes.md", DecisionDeny, RuleUserDeny},
		{"write", "docs/a.md", DecisionAsk, RuleUserAsk},
		{"read", "docs/a.md", DecisionAllow, ""},
	}
	for _, tc := range cases {
		key := "command"
		if tc.tool != "bash" {
			key = "path"
		}
		res := checkCall(t, g, tc.tool, map[string]any{key: tc.arg})
		got := res.RuleID
		if res.Decision == DecisionAsk {
			got = res.Approvals[0].RuleID
		}
		if res.Decision != tc.want || got != tc.rule {
			t.Errorf("%s %q: %s %q, want %s %q (%+v)", tc.tool, tc.arg, res.Decision, got, tc.want, tc.rule, res)
		}
	}
	// A Session grant is an allow and does not override an ask rule.
	g.AllowPathSession(filepath.Join(workspace, "docs"), true, true)
	if res := checkCall(t, g, "write", map[string]any{"path": "docs/a.md"}); res.Decision != DecisionAsk {
		t.Fatalf("session grant overrode ask rule: %+v", res)
	}
}

func TestGuardRulesNeverCoverFloors(t *testing.T) {
	workspace := t.TempDir()
	settings := filepath.Join(workspace, ".aice", "settings.json")
	g, err := New(workspace, Config{ControlFiles: []string{settings}, Rules: Rules{
		Allow: []string{"read(~/.ssh/**)", "write(./.aice/**)", "bash(cat *)"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res := checkCall(t, g, "bash", map[string]any{"command": "cat ~/.ssh/id_rsa"}); res.Decision != DecisionDeny || res.RuleID != credentialsRuleID {
		t.Fatalf("allow rule lifted credentials: %+v", res)
	}
	if res := checkCall(t, g, "write", map[string]any{"path": settings}); res.Decision != DecisionAsk || res.Approvals[0].RuleID != RuleControlFile {
		t.Fatalf("allow rule lifted control file: %+v", res)
	}
}

func TestGuardAddRuleAndSuggestions(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	for cmd, want := range map[string]string{
		"git push origin main":    "bash(git push *)",
		"git -C . push":           "bash(git push *)",
		"gh pr create --title x":  "bash(gh pr create *)",
		"npm publish --tag next":  "bash(npm publish *)",
		"git reset --hard HEAD~1": "bash(git reset *)",
		"curl -d x https://e.com": "bash(curl *)",
		"sudo ls":                 "",
		"rm -rf ../x":             "",
		"git push && npm publish": "",
		"ls":                      "",
	} {
		got, ok := g.SuggestCommandRule(cmd)
		if ok != (want != "") || got != want {
			t.Errorf("SuggestCommandRule(%q) = %q, %v; want %q", cmd, got, ok, want)
		}
	}
	if err := g.AddRule(RuleDecisionAllow, "bash(git push *)"); err != nil {
		t.Fatal(err)
	}
	if res := checkCall(t, g, "bash", map[string]any{"command": "git push origin dev"}); res.Decision != DecisionAllow {
		t.Fatalf("added rule: %+v", res)
	}
	if rule, ok := SuggestPathRule(filepath.Join(t.TempDir(), "x"), true); !ok || !strings.HasPrefix(rule, "write(") || !strings.HasSuffix(rule, "/x/**)") {
		t.Fatalf("SuggestPathRule = %q, %v", rule, ok)
	}
	if _, ok := SuggestPathRule("/", false); ok {
		t.Fatal("SuggestPathRule accepted root")
	}
}
