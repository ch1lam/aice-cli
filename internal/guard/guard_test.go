package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func toolCall(name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	return llm.ToolCall{ID: "test", Name: name, Arguments: raw}
}

func newTestGuard(t *testing.T, workspace string, readOnlyRoots ...string) *Guard {
	t.Helper()
	g, err := New(workspace, Config{ReadOnlyRoots: readOnlyRoots})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// writeFiles creates files below dir; the secret-file policy applies only to
// files that exist.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func checkCall(t *testing.T, g *Guard, name string, args map[string]any) Result {
	t.Helper()
	res, err := g.Check(context.Background(), toolCall(name, args))
	if err != nil {
		t.Fatalf("Check(%s %v): %v", name, args, err)
	}
	return res
}

func hasApproval(res Result, ruleID string) bool {
	for _, approval := range res.Approvals {
		if approval.RuleID == ruleID {
			return true
		}
	}
	return false
}

func TestGuard_AllowNonSecretFiles(t *testing.T) {
	workspace := t.TempDir()
	cases := []string{"README.md", "src/main.go", ".envrc", "config.yaml"}
	writeFiles(t, workspace, cases...)
	g := newTestGuard(t, workspace)
	for _, p := range cases {
		if res := checkCall(t, g, "read", map[string]any{"path": p}); res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow", p, res.Decision)
		}
	}
}

func TestGuard_DenySecretFiles(t *testing.T) {
	workspace := t.TempDir()
	secrets := []string{".env", ".env.local", ".env.production", ".env.prod", ".dev.vars", "a/b/.env", "src/.env"}
	writeFiles(t, workspace, secrets...)
	g := newTestGuard(t, workspace)
	for _, p := range secrets {
		res := checkCall(t, g, "read", map[string]any{"path": p})
		if res.Decision != DecisionDeny || res.RuleID != secretFilesRuleID {
			t.Fatalf("Check(%q) = %v %q, want deny secret-files", p, res.Decision, res.RuleID)
		}
	}
}

func TestGuard_SecretTemplatesAllowed(t *testing.T) {
	workspace := t.TempDir()
	templates := []string{".env.example", "foo.example.env", "bar.sample.env", ".env.test"}
	writeFiles(t, workspace, templates...)
	g := newTestGuard(t, workspace)
	for _, p := range templates {
		if res := checkCall(t, g, "read", map[string]any{"path": p}); res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow", p, res.Decision)
		}
	}
}

func TestGuard_SecretFileMustExist(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	if res := checkCall(t, g, "write", map[string]any{"path": ".env"}); res.Decision != DecisionAllow {
		t.Fatalf("missing .env: %v, want allow", res.Decision)
	}
}

func TestGuard_UnresolvedConservativelyDenies(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	// $VAR expansion cannot prove absence.
	if res := checkCall(t, g, "read", map[string]any{"path": "$HOME/.env"}); res.Decision != DecisionDeny {
		t.Fatalf("Check unresolved: %v, want deny", res.Decision)
	}
}

func TestGuard_BashPathExtraction(t *testing.T) {
	workspace := t.TempDir()
	writeFiles(t, workspace, ".env", "README.md")
	g := newTestGuard(t, workspace)
	if res := checkCall(t, g, "bash", map[string]any{"command": "cat .env"}); res.Decision != DecisionDeny {
		t.Fatalf("bash cat .env: %v want deny", res.Decision)
	}
	if res := checkCall(t, g, "bash", map[string]any{"command": "cat README.md"}); res.Decision != DecisionAllow {
		t.Fatalf("bash cat README: %v want allow", res.Decision)
	}
}

func TestGuard_SessionAllow(t *testing.T) {
	workspace := t.TempDir()
	writeFiles(t, workspace, ".env")
	g := newTestGuard(t, workspace)
	args := map[string]any{"path": ".env"}
	if res := checkCall(t, g, "read", args); res.Decision != DecisionDeny {
		t.Fatalf("pre: %v want deny", res.Decision)
	}
	g.AllowSession(".env")
	if res := checkCall(t, g, "read", args); res.Decision != DecisionAllow {
		t.Fatalf("post allow: %v want allow", res.Decision)
	}
}

func TestNormalizeFilePath(t *testing.T) {
	cases := map[string]string{"./a//b": "a/b", "a\\b": "a/b", "././a": "a"}
	for in, want := range cases {
		if got := normalizeFilePath(in); got != want {
			t.Fatalf("normalize %q = %q want %q", in, got, want)
		}
	}
}

func TestIsBashRootedPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		toolName string
		want     bool
	}{
		{name: "bash root", path: "/tmp/file", toolName: "bash", want: true},
		{name: "bash relative", path: "tmp/file", toolName: "bash", want: false},
		{name: "file tool", path: "/tmp/file", toolName: "read", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isBashRootedPath(test.path, test.toolName); got != test.want {
				t.Fatalf("isBashRootedPath(%q, %q) = %v, want %v", test.path, test.toolName, got, test.want)
			}
		})
	}
}

func TestMaybePathLike(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "windows drive env", in: `C:\Users\x\secret.env`, want: true},
		{name: "windows drive no dot", in: `C:\Users\x\secret`, want: true},
		{name: "drive relative", in: `C:secret`, want: true},
		{name: "unc share", in: `\\server\share\file`, want: true},
		{name: "backslash components", in: `foo\bar`, want: true},
		{name: "single escape", in: `\d`, want: false},
		{name: "tilde", in: `~/x`, want: true},
		{name: "slash", in: `foo/bar`, want: true},
		{name: "dotfile", in: `.env`, want: true},
		{name: "number", in: `3.14`, want: false},
		{name: "plain", in: `plain`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maybePathLike(tt.in); got != tt.want {
				t.Fatalf("maybePathLike(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGuard_DangerousCommandAsks(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	cases := []string{
		"rm -rf build",
		"sudo apt update",
		"dd of=disk.img if=zero.img",
		"chmod -R 777 build",
		"docker run --privileged nginx",
		"shred file.txt",
	}
	for _, cmd := range cases {
		res := checkCall(t, g, "bash", map[string]any{"command": cmd})
		if res.Decision != DecisionAsk || !hasApproval(res, "permissionGate.dangerous") {
			t.Fatalf("dangerous %q: %+v want dangerous ask", cmd, res)
		}
	}
	if res := checkCall(t, g, "bash", map[string]any{"command": "ls -la"}); res.Decision != DecisionAllow {
		t.Fatalf("safe ls: %v want allow", res.Decision)
	}
}

func TestGuard_PathAccess(t *testing.T) {
	workspace := t.TempDir()
	g := newTestGuard(t, workspace)
	if res := checkCall(t, g, "read", map[string]any{"path": "inside.txt"}); res.Decision != DecisionAllow {
		t.Fatalf("inside: %v want allow", res.Decision)
	}
	outside := filepath.Join(t.TempDir(), "nested", "outside.txt")
	res := checkCall(t, g, "read", map[string]any{"path": outside})
	if res.Decision != DecisionAsk || res.Approvals[0].RuleID != "pathAccess.ask" {
		t.Fatalf("outside: %+v want pathAccess.ask", res)
	}
	reason := res.Approvals[0].Reason
	if strings.Contains(reason, `\`) {
		t.Fatalf("Reason uses backslash: %q", reason)
	}
	if display := resolveForDisplay(filepath.Clean(outside), workspace); !strings.Contains(reason, display) {
		t.Fatalf("Reason %q does not contain display path %q", reason, display)
	}
}

func TestGuard_PathAccess_SessionAllow(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "aice-guard-session.txt")
	args := map[string]any{"path": outside}
	if res := checkCall(t, g, "read", args); res.Decision != DecisionAsk {
		t.Fatalf("pre: %v", res.Decision)
	}
	g.AllowPathSession(outside, false)
	if res := checkCall(t, g, "read", args); res.Decision != DecisionAllow {
		t.Fatalf("post session file allow: %v", res.Decision)
	}
	outsideDir := t.TempDir()
	dirArgs := map[string]any{"path": filepath.Join(outsideDir, "a", "b.txt")}
	if res := checkCall(t, g, "read", dirArgs); res.Decision != DecisionAsk {
		t.Fatalf("pre dir: %v", res.Decision)
	}
	g.AllowPathSession(outsideDir, true)
	if res := checkCall(t, g, "read", dirArgs); res.Decision != DecisionAllow {
		t.Fatalf("post dir allow: %v", res.Decision)
	}
}

func TestGuard_PathAccess_SessionAllowCaseFold(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("path grants are case-insensitive only on windows")
	}
	g := newTestGuard(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "aice-guard-case.txt")
	g.AllowPathSession(outside, false)
	alt := strings.ToUpper(outside)
	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": alt}))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("case-folded grant: %v want allow", res.Decision)
	}
}

func TestExtractActionsWindowsDrivePath(t *testing.T) {
	call := toolCall("bash", map[string]any{"command": `cat 'C:\Users\x\secret.env'`})
	acts := extractActions(call)
	found := false
	for _, act := range acts {
		if act.Path == `C:\Users\x\secret.env` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("extractActions missing drive path: %#v", acts)
	}

	call = toolCall("bash", map[string]any{"command": `cat 'C:\Users\x\secret'`})
	acts = extractActions(call)
	found = false
	for _, act := range acts {
		if act.Path == `C:\Users\x\secret` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("extractActions missing drive path without dot: %#v", acts)
	}
}

func TestGuard_PathAccess_SessionAllowFileDoesNotGrantSibling(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	outsideDir := t.TempDir()
	granted := filepath.Join(outsideDir, "granted.txt")
	sibling := filepath.Join(outsideDir, "sibling.txt")

	g.AllowPathSession(granted, false)

	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": granted}))
	if err != nil {
		t.Fatalf("Check(granted): %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("granted file: %v want allow", res.Decision)
	}

	res, err = g.Check(context.Background(), toolCall("read", map[string]any{"path": sibling}))
	if err != nil {
		t.Fatalf("Check(sibling): %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("sibling file: %v want ask (Allow this file for this run grants the path, not the parent)", res.Decision)
	}
}

func TestGuard_PathAccess_RejectsRootAndHomeSessionGrant(t *testing.T) {
	workspace := t.TempDir()
	g := newTestGuard(t, workspace)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	g.AllowPathSession("/", false)
	g.AllowPathSession("/", true)
	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if err != nil {
		t.Fatalf("Check after root grant: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("root grant: %v want ask", res.Decision)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory is unavailable")
	}
	homeFile := filepath.Join(home, "aice-guard-too-broad.txt")
	if hostpath.Within(workspace, homeFile) {
		t.Skip("workspace covers home; cannot observe a too-broad home grant")
	}
	g.AllowPathSession(home, false)
	g.AllowPathSession(home, true)
	res, err = g.Check(context.Background(), toolCall("read", map[string]any{"path": homeFile}))
	if err != nil {
		t.Fatalf("Check after home grant: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("home grant: %v want ask", res.Decision)
	}

	g.AllowPathSession(outside, false)
	res, err = g.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if err != nil {
		t.Fatalf("Check after exact grant: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("exact path grant: %v want allow", res.Decision)
	}
}

func TestGuard_StructuralDangerousMatching(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	cases := map[string]bool{
		// rm variants that a substring "rm -rf" would miss
		"rm -R -f build":                true,
		"rm --recursive --force build":  true,
		"rm -r -f build":                true,
		"rm -Rfv build":                 true,
		"rm -fr build":                  true,
		"chmod -R 777 build":            true,
		"chmod -R 755 build":            false,
		"chmod 777 file":                false,
		"docker run --privileged nginx": true,
		"docker build --privileged .":   false,
		// structural matching ignores words that are only arguments
		"echo rm -rf build":     false,
		`echo "rm -rf"`:         false,
		"cat a | sudo rm -rf b": true,
	}
	for cmd, dangerous := range cases {
		res := checkCall(t, g, "bash", map[string]any{"command": cmd})
		if got := hasApproval(res, "permissionGate.dangerous"); got != dangerous {
			t.Fatalf("%q dangerous = %v, want %v (%+v)", cmd, got, dangerous, res)
		}
		if dangerous && res.Approvals[0].Pattern == "" {
			t.Fatalf("%q: dangerous approval has no pattern", cmd)
		}
	}
}

func TestGuard_UnknownToolAsks(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	cases := []string{"mystery_tool", "Read", "read_file", "custom"}
	for _, name := range cases {
		res, err := g.Check(context.Background(), toolCall(name, map[string]any{"path": "README.md"}))
		if err != nil {
			t.Fatalf("Check(%q): %v", name, err)
		}
		if res.Decision != DecisionAsk {
			t.Fatalf("Check(%q) = %v, want ask", name, res.Decision)
		}
		if res.Approvals[0].RuleID != "unknownTool" {
			t.Fatalf("Check(%q) rule %q, want unknownTool", name, res.RuleID)
		}
		if !strings.Contains(res.Approvals[0].Reason, "not recognized") {
			t.Fatalf("Check(%q) reason %q, want not recognized", name, res.Reason)
		}
		if res.Approvals[0].Action.ToolName != name {
			t.Fatalf("Check(%q) action tool %q", name, res.Approvals[0].Action.ToolName)
		}
	}
}

func TestGuard_KnownToolWithoutRestrictedActionAllows(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	cases := []llm.ToolCall{
		toolCall("read", map[string]any{}),
		toolCall("write", map[string]any{"content": "x"}),
		toolCall("bash", map[string]any{}),
		toolCall("bash", map[string]any{"command": ""}),
		toolCall("bash", map[string]any{"command": "echo hello"}),
		toolCall("skill", map[string]any{}),
		toolCall("skill", map[string]any{"name": "pdf"}),
		toolCall("skill", map[string]any{"name": "/etc/passwd"}),
		toolCall("request_user_input", map[string]any{}),
	}
	for _, call := range cases {
		res, err := g.Check(context.Background(), call)
		if err != nil {
			t.Fatalf("Check(%q): %v", call.Name, err)
		}
		if res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow", call.Name, res.Decision)
		}
		if res.RuleID == "unknownTool" {
			t.Fatalf("Check(%q) treated as unknownTool", call.Name)
		}
	}
}

func TestGuard_SkillToolNotBlockedBySecretFiles(t *testing.T) {
	workspace := t.TempDir()
	writeFiles(t, workspace, ".env")
	g := newTestGuard(t, workspace)
	if res := checkCall(t, g, "skill", map[string]any{"name": ".env"}); res.Decision != DecisionAllow {
		t.Fatalf("skill named .env: %v want allow", res.Decision)
	}
}

func TestGuard_ReadOnlyRoots(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()
	inside := filepath.Join(skillDir, "references", "guide.md")
	outside := filepath.Join(t.TempDir(), "other.txt")
	writeFiles(t, skillDir, ".env")
	g := newTestGuard(t, workspace, skillDir)

	readTools := []string{"read", "grep", "find", "ls"}
	for _, name := range readTools {
		res, err := g.Check(context.Background(), toolCall(name, map[string]any{"path": inside}))
		if err != nil {
			t.Fatalf("Check %s inside: %v", name, err)
		}
		if res.Decision != DecisionAllow {
			t.Fatalf("Check %s inside root: %v want allow", name, res.Decision)
		}
		res, err = g.Check(context.Background(), toolCall(name, map[string]any{"path": outside}))
		if err != nil {
			t.Fatalf("Check %s outside: %v", name, err)
		}
		if res.Decision != DecisionAsk || res.Approvals[0].RuleID != "pathAccess.ask" {
			t.Fatalf("Check %s outside root: %v %q want ask", name, res.Decision, res.RuleID)
		}
	}

	for _, name := range []string{"write", "edit"} {
		res, err := g.Check(context.Background(), toolCall(name, map[string]any{"path": inside}))
		if err != nil {
			t.Fatalf("Check %s inside: %v", name, err)
		}
		if res.Decision != DecisionAsk || res.Approvals[0].RuleID != "pathAccess.ask" {
			t.Fatalf("Check %s inside root: %v %q want ask", name, res.Decision, res.RuleID)
		}
	}

	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": "inside.txt"}))
	if err != nil {
		t.Fatalf("Check workspace read: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("workspace read: %v want allow", res.Decision)
	}

	secret := filepath.Join(skillDir, ".env")
	res, err = g.Check(context.Background(), toolCall("read", map[string]any{"path": secret}))
	if err != nil {
		t.Fatalf("Check secret in root: %v", err)
	}
	if res.Decision != DecisionDeny || res.RuleID != "secret-files" {
		t.Fatalf("secret in read-only root: %v %q want deny secret-files", res.Decision, res.RuleID)
	}

	g.AllowPathSession(inside, false)
	res, err = g.Check(context.Background(), toolCall("write", map[string]any{"path": inside}))
	if err != nil {
		t.Fatalf("Check write after grant: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("write after path grant: %v want allow", res.Decision)
	}
}

func TestGuard_PathExtractionAST(t *testing.T) {
	g := newTestGuard(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "aice-ast-redirect.txt")
	res := checkCall(t, g, "bash", map[string]any{"command": "echo hi > " + filepath.ToSlash(outside)})
	if res.Decision != DecisionAsk || !hasApproval(res, "pathAccess.ask") {
		t.Fatalf("redirect outside: %+v want path ask", res)
	}
	// $HOME/.env is unresolved and cannot be proven absent.
	if res := checkCall(t, g, "bash", map[string]any{"command": "cat $HOME/.env"}); res.Decision != DecisionDeny {
		t.Fatalf("$HOME expansion: %v want deny", res.Decision)
	}
}

func TestGuard_AllowToolSession(t *testing.T) {
	g := newTestGuard(t, t.TempDir())

	res, err := g.Check(context.Background(), toolCall("mystery_tool", map[string]any{"q": "x"}))
	if err != nil {
		t.Fatalf("Check before grant: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("unknown tool before grant: %v want ask", res.Decision)
	}
	if res.Approvals[0].RuleID != "unknownTool" {
		t.Fatalf("rule %q want unknownTool", res.RuleID)
	}

	g.AllowToolSession("mystery_tool")

	res, err = g.Check(context.Background(), toolCall("mystery_tool", map[string]any{"q": "x"}))
	if err != nil {
		t.Fatalf("Check after grant: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("granted unknown tool: %v want allow", res.Decision)
	}

	res, err = g.Check(context.Background(), toolCall("custom", map[string]any{"q": "x"}))
	if err != nil {
		t.Fatalf("Check other unknown: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("ungranted unknown tool: %v want ask", res.Decision)
	}
	if res.Approvals[0].RuleID != "unknownTool" {
		t.Fatalf("ungranted rule %q want unknownTool", res.RuleID)
	}
}

func TestGrantTooBroad(t *testing.T) {
	type testCase struct {
		name string
		path string
		want bool
	}
	cases := []testCase{
		{name: "dot", path: ".", want: false},
		{name: "relative dir", path: "relative/dir", want: false},
		{name: "ordinary directory", path: t.TempDir(), want: false},
	}
	if runtime.GOOS == "windows" {
		volRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
		cases = append(cases,
			testCase{name: "filesystem root", path: volRoot, want: true},
			testCase{name: "windows slash root", path: "/", want: true},
			testCase{name: "windows backslash root", path: `\`, want: true},
			testCase{name: "windows drive root", path: `C:\`, want: true},
			testCase{name: "windows unc share root", path: `\\srv\share`, want: true},
			testCase{name: "windows unc share child", path: `\\srv\share\dir`, want: false},
			testCase{name: "windows drive child", path: `C:\Windows`, want: false},
		)
	} else {
		cases = append(cases, testCase{name: "filesystem root", path: "/", want: true})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cases = append(cases,
			testCase{name: "home directory", path: home, want: true},
			testCase{name: "home child", path: filepath.Join(home, "subdir"), want: false},
		)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GrantTooBroad(tc.path); got != tc.want {
				t.Fatalf("GrantTooBroad(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestFilesystemRootDirIdentity(t *testing.T) {
	roots := []string{"/"}
	if runtime.GOOS == "windows" {
		roots = append(roots,
			filepath.VolumeName(t.TempDir())+string(filepath.Separator),
			`\`,
			`C:\`,
			`\\srv\share`,
		)
	}
	for _, root := range roots {
		cleaned := filepath.Clean(root)
		if got := filepath.Dir(cleaned); got != cleaned {
			t.Errorf("filepath.Dir(%q) = %q, want identity (filesystem root)", cleaned, got)
		}
	}

	dot := filepath.Clean(".")
	if got := filepath.Dir(dot); got != dot {
		t.Fatalf("filepath.Dir(%q) = %q, want identity", dot, got)
	}
	if GrantTooBroad(".") {
		t.Fatal(`GrantTooBroad(".") = true, want false ("." has Dir identity but is not a root)`)
	}
}
