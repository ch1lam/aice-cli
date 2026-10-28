package guard

import "testing"

func TestGuardBashHints(t *testing.T) {
	g, err := New(t.TempDir(), Config{Rules: Rules{Allow: []string{"bash(go test *)", "bash(python *)", "bash(git push *)"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.AllowCommandSession("npm run build")
	for _, tc := range []struct {
		command           string
		readOnly, covered bool
	}{
		{"cat go.mod | grep module", true, true},
		{"git status && git diff --stat", true, true},
		{"git branch", true, true},
		{"git branch -m old new", false, false},
		{"timeout 5 rg TODO internal", true, true},
		{"find . -name '*.go'", true, true},
		{"find . -name '*.go' -exec gofmt -w {} +", false, false},
		{"sed -n 1,10p a.go", true, true},
		{"sed -i '' s/a/b/ a.go", false, false},
		{"echo hi > out.txt", false, true},
		{"go test ./...", false, true},
		{"go test ./... && make", false, false},
		{"python3 -c 'print(1)'", false, false},
		{"python script.py", false, false}, // broad rule bash(python *) is ignored
		{"npm run build", false, true},     // exact Session grant
		{"bash -c 'ls'", false, false},
		{"echo x > .git/hooks/pre-commit", false, false},
	} {
		res := checkCall(t, g, "bash", map[string]any{"command": tc.command})
		if res.Decision != DecisionAllow {
			t.Fatalf("%q: %+v, want allow", tc.command, res)
		}
		if res.ReadOnly != tc.readOnly || res.RuleCovered != tc.covered {
			t.Errorf("%q: readOnly=%v covered=%v, want %v %v", tc.command, res.ReadOnly, res.RuleCovered, tc.readOnly, tc.covered)
		}
	}
	// Covered findings: the allow rule drops the outbound finding, and the
	// command is covered by a non-broad rule.
	if res := checkCall(t, g, "bash", map[string]any{"command": "git push origin dev"}); res.Decision != DecisionAllow || res.ReadOnly || !res.RuleCovered {
		t.Fatalf("git push with allow rule: %+v", res)
	}
}
