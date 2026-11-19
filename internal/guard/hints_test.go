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
		{"echo x > vendor/lib/.git/hooks/post-checkout", false, false},
		// Read-only looking commands that run code or write files.
		{"git -c core.fsmonitor=./x status", false, false},
		{"git --config-env=core.pager=P log", false, false},
		{"git grep -O./x TODO", false, false},
		{"git diff --output=d.txt", false, false},
		{"rg --pre ./x TODO", false, false},
		{"rg --pre-glob '*.gz' TODO", false, false},
		{"sed 'e date' a.txt", false, false},
		{"sed -n 's/a/b/e' a.txt", false, false},
		{"sed 's/a/b/w out.txt' a.txt", false, true},
		{"sed -e p -e 'w out.txt' a.txt", false, true},
		{"sed '1r other.txt' a.txt", true, true},
		{"sed -f x.sed a.txt", false, false},
		{"sed -n '/^func /,/^}/p' a.go", true, true},
		{"sed 's/hello/world/g' a.txt", true, true},
		{"LD_PRELOAD=./x.so cat a.txt", false, false},
		{"DYLD_INSERT_LIBRARIES=./x cat a.txt", false, false},
		{"env PATH=. cat a.txt", false, false},
		{"export GIT_CONFIG_COUNT=1; git status", false, false},
		{"LC_ALL=C grep -n x a.txt", true, true},
		{"GIT_PAGER=cat git log -1", true, true},
		{"f=go.mod; cat $f", true, true},
		{"uniq a.txt b.txt", false, true},
		{"tree -o out.txt", false, true},
		{"less -o log.txt a.txt", false, true},
		{"less -Olog.txt a.txt", false, true},
		{"cat $(cat list.txt)", false, false},
		{"wc -l < $(echo a.txt)", false, false},
		{"printf -v PATH %s .", false, false},
		{"echo $(date)", true, true},
		{"trap 'echo done' EXIT; cat a.txt", false, false},
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
	for path, want := range map[string]bool{".git/config": true, "vendor/x/.git/hooks/pre-push": true, "a.go": false, ".github/ci.yml": false} {
		if res := checkCall(t, g, "write", map[string]any{"path": path}); res.GitWrite != want {
			t.Errorf("write %s: GitWrite=%v, want %v", path, res.GitWrite, want)
		}
	}
}
