package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestDiscoverSkillsLoadsBuiltinAndUser(t *testing.T) {
	t.Parallel()

	userRoot := filepath.Join(t.TempDir(), "skills")
	writeTestSkill(t, userRoot, "user-demo", "User-level demo skill.")

	discovery := discoverSkills(userRoot, "", false)
	if _, ok := discovery.catalog.Lookup("create-skill"); !ok {
		t.Fatal("catalog missing builtin create-skill")
	}
	if _, ok := discovery.catalog.Lookup("user-demo"); !ok {
		t.Fatal("catalog missing user-demo")
	}
}

func TestDiscoverSkillsOmitsUntrustedProject(t *testing.T) {
	t.Parallel()

	userRoot := filepath.Join(t.TempDir(), "user")
	projectRoot := filepath.Join(t.TempDir(), "project")
	writeTestSkill(t, userRoot, "user-demo", "User-level demo skill.")
	writeTestSkill(t, projectRoot, "project-demo", "Project-level demo skill.")

	untrusted := discoverSkills(userRoot, projectRoot, false)
	if _, ok := untrusted.catalog.Lookup("project-demo"); ok {
		t.Fatal("untrusted catalog included project-demo")
	}
	if _, ok := untrusted.catalog.Lookup("user-demo"); !ok {
		t.Fatal("untrusted catalog missing user-demo")
	}

	trusted := discoverSkills(userRoot, projectRoot, true)
	if _, ok := trusted.catalog.Lookup("project-demo"); !ok {
		t.Fatal("trusted catalog missing project-demo")
	}
}

func TestDiscoverSkillsScanFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	discovery := discoverSkills(notDir, "", false)
	if _, ok := discovery.catalog.Lookup("create-skill"); !ok {
		t.Fatal("catalog missing builtin create-skill after scan error")
	}
	if !hasSkillDiagnostic(discovery.diags, skill.LevelError, notDir, "") {
		t.Fatalf("diags = %#v, want error for %q", discovery.diags, notDir)
	}
}

func TestFormatSkillsPrompt(t *testing.T) {
	t.Parallel()

	if got := formatSkillsPrompt(skill.Catalog{}); got != "" {
		t.Fatalf("empty catalog prompt = %q, want empty", got)
	}

	catalog, diags := skill.Merge([]skill.Skill{
		{
			Name:        "zeta",
			Description: "Last skill.",
			Source:      skill.SourceUser,
		},
		{
			Name:        "alpha",
			Description: "First skill.",
			Source:      skill.SourceProject,
		},
	})
	if len(diags) != 0 {
		t.Fatalf("Merge() diags = %#v, want none", diags)
	}

	got := formatSkillsPrompt(catalog)
	want := "Available Agent Skills:\n" +
		"The following skills provide specialized instructions for specific tasks.\n" +
		"When a task matches a skill's description, call the skill tool with the\n" +
		"skill's name to load its full instructions.\n" +
		"<available_skills>\n" +
		"- alpha: First skill.\n" +
		"- zeta: Last skill.\n" +
		"</available_skills>"
	if got != want {
		t.Fatalf("formatSkillsPrompt() = %q, want %q", got, want)
	}
	if strings.Contains(got, string(skill.SourceProject)) ||
		strings.Contains(got, string(skill.SourceUser)) {
		t.Fatalf("skills prompt leaked source labels: %q", got)
	}
}

func TestAppendSkillTool(t *testing.T) {
	t.Parallel()

	workspace := testWorkspace(t, t.TempDir())
	base := testBuiltInTools(t, workspace)
	if slices.Contains(toolNames(base), "skill") {
		t.Fatal("built-in tools unexpectedly included skill")
	}

	without := appendSkillTool(base, skill.Catalog{})
	if slices.Contains(toolNames(without), "skill") {
		t.Fatal("empty catalog registered skill tool")
	}

	catalog, diags := skill.Merge([]skill.Skill{
		{Name: "zeta", Body: "z"},
		{Name: "alpha", Body: "a"},
	})
	if len(diags) != 0 {
		t.Fatalf("Merge() diags = %#v, want none", diags)
	}
	with := appendSkillTool(base, catalog)
	if !slices.Equal(toolNames(with), append(toolNames(base), "skill")) {
		t.Fatalf("tools = %v, want skill appended", toolNames(with))
	}
	if got, want := skillNameEnum(t, with), []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("skill enum = %v, want %v", got, want)
	}
}

func TestAssembleSystemPromptSkills(t *testing.T) {
	t.Parallel()

	t.Run("empty catalog omits section", func(t *testing.T) {
		t.Parallel()
		workspace := testWorkspace(t, t.TempDir())
		prompt, err := assembleSystemPrompt(
			workspace,
			trustTestConfig(trustTestPaths(t)),
			trust.DecisionUntrusted,
			testBuiltInTools(t, workspace),
			skill.Catalog{},
		)
		if err != nil {
			t.Fatalf("assembleSystemPrompt() error = %v", err)
		}
		if strings.Contains(prompt, "Available Agent Skills:") {
			t.Fatalf("empty catalog injected skills section: %q", prompt)
		}
	})

	t.Run("custom system still appends list", func(t *testing.T) {
		t.Parallel()
		workspace := testWorkspace(t, t.TempDir())
		paths := trustTestPaths(t)
		writeAppFile(t, filepath.Dir(paths.GlobalSettings), "SYSTEM.md", "custom global base")
		prompt, err := assembleSystemPrompt(
			workspace,
			trustTestConfig(paths),
			trust.DecisionUntrusted,
			testBuiltInTools(t, workspace),
			builtinSkillCatalog(t),
		)
		if err != nil {
			t.Fatalf("assembleSystemPrompt() error = %v", err)
		}
		if !strings.HasPrefix(prompt, "custom global base\n\nAvailable Agent Skills:") {
			t.Fatalf("prompt = %q, want custom base then skills list", prompt)
		}
		if !strings.Contains(prompt, "- create-skill: ") {
			t.Fatalf("prompt missing create-skill: %q", prompt)
		}
	})
}

func TestNewRunEnvironmentTrustedProjectListsSkills(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	workspace := t.TempDir()
	writeTestSkill(
		t,
		filepath.Join(home, filepath.FromSlash(trust.SkillsDir)),
		"user-demo",
		"User-level demo skill.",
	)
	writeTestSkill(
		t,
		filepath.Join(workspace, filepath.FromSlash(trust.SkillsDir)),
		"project-demo",
		"Project-level demo skill.",
	)

	env := startSkillRun(t, home, workspace, boolPtr(true))
	assertSkillListed(t, env.systemPrompt, "create-skill")
	assertSkillListed(t, env.systemPrompt, "user-demo")
	assertSkillListed(t, env.systemPrompt, "project-demo")
	if !slices.Contains(toolNames(env.tools), "skill") {
		t.Fatalf("tools = %v, want skill", toolNames(env.tools))
	}
	got := skillNameEnum(t, env.tools)
	for _, name := range []string{"create-skill", "project-demo", "user-demo"} {
		if !slices.Contains(got, name) {
			t.Fatalf("skill enum = %v, want %q", got, name)
		}
	}
}

func TestNewRunEnvironmentUntrustedOmitsProjectSkill(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	workspace := t.TempDir()
	writeTestSkill(
		t,
		filepath.Join(home, filepath.FromSlash(trust.SkillsDir)),
		"user-demo",
		"User-level demo skill.",
	)
	writeTestSkill(
		t,
		filepath.Join(workspace, filepath.FromSlash(trust.SkillsDir)),
		"project-demo",
		"Project-level demo skill.",
	)

	env := startSkillRun(t, home, workspace, boolPtr(false))
	assertSkillListed(t, env.systemPrompt, "create-skill")
	assertSkillListed(t, env.systemPrompt, "user-demo")
	if strings.Contains(env.systemPrompt, "project-demo") {
		t.Fatalf("untrusted prompt listed project skill: %q", env.systemPrompt)
	}
	got := skillNameEnum(t, env.tools)
	if slices.Contains(got, "project-demo") {
		t.Fatalf("untrusted skill enum = %v, want project-demo omitted", got)
	}
	if !slices.Contains(got, "user-demo") {
		t.Fatalf("untrusted skill enum = %v, want user-demo", got)
	}
}

func TestNewRunEnvironmentCustomGlobalSystemAppendsSkills(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	workspace := t.TempDir()
	app, paths := newSkillRunApp(t, home)
	writeAppFile(t, filepath.Dir(paths.GlobalSettings), "SYSTEM.md", "custom global base")

	env, err := app.newRunEnvironment(t.Context(), workspace, nil, nil, false)
	if err != nil {
		t.Fatalf("newRunEnvironment() error = %v", err)
	}
	if !strings.HasPrefix(env.systemPrompt, "custom global base\n\nAvailable Agent Skills:") {
		t.Fatalf("prompt = %q, want custom base then skills list", env.systemPrompt)
	}
	assertSkillListed(t, env.systemPrompt, "create-skill")
}

func TestNewRunEnvironmentReadOnlyRootsIncludeDiskSkills(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	workspace := t.TempDir()
	skillDir := writeTestSkill(
		t,
		filepath.Join(home, filepath.FromSlash(trust.SkillsDir)),
		"user-demo",
		"User-level demo skill.",
	)
	guide := filepath.Join(skillDir, "references", "guide.md")
	writeAppFile(t, filepath.Dir(guide), "guide.md", "bundled")
	outside := filepath.Join(t.TempDir(), "other.txt")

	env := startSkillRun(t, home, workspace, nil)
	assertPathDecision(t, env.guard, guide, guard.DecisionAllow)
	assertPathDecision(t, env.guard, outside, guard.DecisionAsk)

	writeResult, err := env.guard.Check(t.Context(), llm.ToolCall{
		ID:        "write-1",
		Name:      "write",
		Arguments: mustSkillPathArgs(t, guide),
	})
	if err != nil {
		t.Fatalf("Check(write) error = %v", err)
	}
	if writeResult.Decision != guard.DecisionAsk {
		t.Fatalf("write inside skill dir = %q, want ask", writeResult.Decision)
	}
}

func TestNewRunEnvironmentShadowingRecordsDiagnostic(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	workspace := t.TempDir()
	userDir := writeTestSkill(
		t,
		filepath.Join(home, filepath.FromSlash(trust.SkillsDir)),
		"shared",
		"User shared skill.",
	)
	writeTestSkill(
		t,
		filepath.Join(workspace, filepath.FromSlash(trust.SkillsDir)),
		"shared",
		"Project shared skill.",
	)

	env := startSkillRun(t, home, workspace, boolPtr(true))
	item, ok := env.skills.Lookup("shared")
	if !ok {
		t.Fatal("catalog missing shared")
	}
	if item.Source != skill.SourceProject {
		t.Fatalf("shared Source = %q, want %q", item.Source, skill.SourceProject)
	}
	wantDir := filepath.Join(
		env.workspace.PhysicalPath(),
		filepath.FromSlash(trust.SkillsDir),
		"shared",
	)
	if item.Dir != wantDir {
		t.Fatalf("shared Dir = %q, want project %q", item.Dir, wantDir)
	}
	if item.Description != "Project shared skill." {
		t.Fatalf("shared Description = %q, want project", item.Description)
	}
	if !hasSkillDiagnostic(
		env.skillDiags,
		skill.LevelWarn,
		userDir,
		`skill "shared" from user shadowed by project`,
	) {
		t.Fatalf("skillDiags = %#v, want shadowing warning", env.skillDiags)
	}
	assertSkillListed(t, env.systemPrompt, "shared")
	if strings.Contains(env.systemPrompt, "User shared skill.") {
		t.Fatalf("prompt kept shadowed description: %q", env.systemPrompt)
	}
}

func TestNewRunEnvironmentHomeErrorSkipsUserSkills(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	paths := trustTestPaths(t)
	app := &application{
		dependencies: dependencies{
			loadConfig: func() (config.Config, error) {
				return trustTestConfig(paths), nil
			},
			providers: defaultProviders(),
			userHomeDir: func() (string, error) {
				return "", errors.New("home unavailable")
			},
		},
	}
	env, err := app.newRunEnvironment(t.Context(), workspace, nil, nil, false)
	if err != nil {
		t.Fatalf("newRunEnvironment() error = %v", err)
	}
	if _, ok := env.skills.Lookup("create-skill"); !ok {
		t.Fatal("catalog missing builtin create-skill")
	}
	if !hasSkillDiagnostic(env.skillDiags, skill.LevelError, "", "skip user skills: home unavailable") {
		t.Fatalf("skillDiags = %#v, want home skip diagnostic", env.skillDiags)
	}
}

func TestSkillReadOnlyRootsSkipEmptyDir(t *testing.T) {
	t.Parallel()

	catalog, diags := skill.Merge([]skill.Skill{
		{Name: "embedded", Dir: ""},
		{Name: "disk", Dir: "/tmp/skill"},
	})
	if len(diags) != 0 {
		t.Fatalf("Merge() diags = %#v, want none", diags)
	}
	got := skillReadOnlyRoots(catalog)
	if !slices.Equal(got, []string{"/tmp/skill"}) {
		t.Fatalf("skillReadOnlyRoots() = %v, want disk dir only", got)
	}
}

func startSkillRun(t *testing.T, home, workspace string, override *bool) *runEnvironment {
	t.Helper()
	app, _ := newSkillRunApp(t, home)
	env, err := app.newRunEnvironment(t.Context(), workspace, override, nil, false)
	if err != nil {
		t.Fatalf("newRunEnvironment() error = %v", err)
	}
	return env
}

func newSkillRunApp(t *testing.T, home string) (*application, config.Paths) {
	t.Helper()
	paths := trustTestPaths(t)
	return &application{
		dependencies: dependencies{
			loadConfig: func() (config.Config, error) {
				return trustTestConfig(paths), nil
			},
			providers: defaultProviders(),
			userHomeDir: func() (string, error) {
				return home, nil
			},
		},
	}, paths
}

func writeTestSkill(t *testing.T, skillsRoot, name, description string) string {
	t.Helper()
	dir := filepath.Join(skillsRoot, name)
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s instructions.\n", name, description, name)
	writeAppFile(t, dir, "SKILL.md", content)
	return dir
}

func toolNames(tools []agent.Tool) []string {
	names := make([]string, len(tools))
	for i, current := range tools {
		names[i] = current.Definition().Name
	}
	return names
}

func skillNameEnum(t *testing.T, tools []agent.Tool) []string {
	t.Helper()
	for _, current := range tools {
		definition := current.Definition()
		if definition.Name != "skill" {
			continue
		}
		var schema struct {
			Properties struct {
				Name struct {
					Enum []string `json:"enum"`
				} `json:"name"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatalf("skill schema: %v", err)
		}
		return schema.Properties.Name.Enum
	}
	t.Fatal("skill tool not registered")
	return nil
}

func assertSkillListed(t *testing.T, prompt, name string) {
	t.Helper()
	marker := "- " + name + ": "
	if !strings.Contains(prompt, "Available Agent Skills:") ||
		!strings.Contains(prompt, "<available_skills>") ||
		!strings.Contains(prompt, marker) {
		t.Fatalf("prompt missing skill %q: %q", name, prompt)
	}
}

func hasSkillDiagnostic(diags []skill.Diagnostic, level skill.Level, dir, message string) bool {
	for _, diag := range diags {
		if diag.Level != level {
			continue
		}
		if dir != "" && diag.Dir != dir {
			continue
		}
		if message != "" && diag.Message != message && !strings.Contains(diag.Message, message) {
			continue
		}
		return true
	}
	return false
}

func mustSkillPathArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
