package guard

import (
	"context"
	"encoding/json"
	"testing"

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
		res, err := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": p}))
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
		res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": p}))
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
		res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": p}))
		if res.Decision != DecisionAllow {
			t.Fatalf("Check(%q) = %v, want allow (allowed pattern)", p, res.Decision)
		}
	}
}

func TestGuard_OnlyIfExists_SkipsNonExistent(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, neverExists)
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": ".env"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("Check with neverExists: %v, want allow (file does not exist)", res.Decision)
	}
}

func TestGuard_UnresolvedConservativelyDenies(t *testing.T) {
	g, _ := NewWithExists(t.TempDir(), Config{}, neverExists)
	// $VAR expansion: cannot prove absence, so deny
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": "$HOME/.env"}))
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
	res, _ := g.Check(context.Background(), toolCall("read", map[string]any{"file_path": "LOCKED.md"}))
	if res.Decision != DecisionAllow {
		t.Fatalf("readOnly should allow read: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("write", map[string]any{"file_path": "LOCKED.md"}))
	if res.Decision != DecisionDeny {
		t.Fatalf("readOnly should deny write: %v", res.Decision)
	}
	res, _ = g.Check(context.Background(), toolCall("edit", map[string]any{"path": "LOCKED.md"}))
	// edit uses path key -> extractActions handles file_path/path
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
	call := toolCall("read", map[string]any{"file_path": ".env"})
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
