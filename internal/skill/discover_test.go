package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScanMissingRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "missing")
	skills, diags, err := Scan(os.DirFS(root), SourceUser, root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("Scan() skills = %#v, want empty", skills)
	}
	if len(diags) != 0 {
		t.Fatalf("Scan() diags = %#v, want empty", diags)
	}
}

func TestScanSkipsNonDirectoryAndNestedAndBareDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "not a skill")
	if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, "nested", "deep", "SKILL.md"), validSkillMD("deep", "Nested. Use when nested."))
	writeFile(t, filepath.Join(root, "ok-skill", "SKILL.md"), validSkillMD("ok-skill", "Loads. Use when loading."))
	writeFile(t, filepath.Join(root, "ok-skill", "references", "extra.md"), "ignored")

	skills, diags, err := Scan(os.DirFS(root), SourceProject, root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("Scan() diags = %#v, want empty", diags)
	}
	if len(skills) != 1 {
		t.Fatalf("Scan() skills = %#v, want 1", skills)
	}
	got := skills[0]
	if got.Name != "ok-skill" {
		t.Errorf("Name = %q, want ok-skill", got.Name)
	}
	if got.Source != SourceProject {
		t.Errorf("Source = %q, want %q", got.Source, SourceProject)
	}
	wantDir := filepath.Join(root, "ok-skill")
	if got.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", got.Dir, wantDir)
	}
}

func TestScanOversizedAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "huge", "SKILL.md"), strings.Repeat("a", maxSkillBytes+1))
	writeFileBytes(t, filepath.Join(root, "binary", "SKILL.md"), []byte("\xff\xfe---\nname: binary\n"))

	skills, diags, err := Scan(os.DirFS(root), SourceUser, root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("Scan() skills = %#v, want empty", skills)
	}
	if !hasDiagnostic(diags, LevelError, filepath.Join(root, "huge"), errTooLarge.Error()) {
		t.Errorf("missing oversized diagnostic: %#v", diags)
	}
	if !hasDiagnostic(diags, LevelError, filepath.Join(root, "binary"), errInvalidUTF8.Error()) {
		t.Errorf("missing utf-8 diagnostic: %#v", diags)
	}
}

func TestScanNonRegularSkillFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir-skill", "SKILL.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	skills, diags, err := Scan(os.DirFS(root), SourceUser, root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("Scan() skills = %#v, want empty", skills)
	}
	if !hasDiagnostic(diags, LevelError, filepath.Join(root, "dir-skill"), "not a regular file") {
		t.Fatalf("missing non-regular diagnostic: %#v", diags)
	}
}

func TestScanRecordsWarningsAndUsesFrontmatterName(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"other-dir/SKILL.md": {
			Data: []byte(validSkillMD("pdf-processing", "Extract PDFs. Use when handling PDFs.")),
		},
	}
	skills, diags, err := Scan(fsys, SourceUser, "")
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "pdf-processing" {
		t.Fatalf("Scan() skills = %#v, want name pdf-processing", skills)
	}
	if skills[0].Dir != "" {
		t.Errorf("Dir = %q, want empty", skills[0].Dir)
	}
	if !hasDiagnostic(diags, LevelWarn, "other-dir", "does not match parent directory") {
		t.Fatalf("missing mismatch warning: %#v", diags)
	}
}

func TestScanBuiltinSourceSkipsInvalid(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"broken/SKILL.md": {Data: []byte("# no frontmatter\n")},
	}
	skills, diags, err := Scan(fsys, SourceBuiltin, "")
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("Scan() skills = %#v, want skipped", skills)
	}
	if !hasDiagnostic(diags, LevelError, "broken", errNoFrontmatter.Error()) {
		t.Fatalf("missing skip diagnostic: %#v", diags)
	}
}

func TestMergeShadowingPriority(t *testing.T) {
	t.Parallel()

	builtin := []Skill{{
		Name:        "shared",
		Description: "builtin copy",
		Source:      SourceBuiltin,
		Body:        "builtin body",
	}}
	user := []Skill{{
		Name:        "shared",
		Description: "user copy",
		Source:      SourceUser,
		Dir:         "/home/me/.agents/skills/shared",
		Body:        "user body",
	}, {
		Name:        "user-only",
		Description: "only user",
		Source:      SourceUser,
		Dir:         "/home/me/.agents/skills/user-only",
		Body:        "user only",
	}}
	project := []Skill{{
		Name:        "shared",
		Description: "project copy",
		Source:      SourceProject,
		Dir:         "/repo/.agents/skills/shared",
		Body:        "project body",
	}, {
		Name:        "alpha",
		Description: "project alpha",
		Source:      SourceProject,
		Dir:         "/repo/.agents/skills/alpha",
		Body:        "alpha",
	}}

	catalog, diags := Merge(builtin, user, project)
	got := catalog.Skills()
	if len(got) != 3 {
		t.Fatalf("Skills() = %#v, want 3", got)
	}
	if got[0].Name != "alpha" || got[1].Name != "shared" || got[2].Name != "user-only" {
		t.Fatalf("order = %q %q %q, want alpha shared user-only", got[0].Name, got[1].Name, got[2].Name)
	}
	shared, ok := catalog.Lookup("shared")
	if !ok {
		t.Fatal("Lookup(shared) missing")
	}
	if shared.Source != SourceProject || shared.Body != "project body" {
		t.Fatalf("Lookup(shared) = %#v, want project copy", shared)
	}
	if _, ok := catalog.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) = true, want false")
	}

	if !hasDiagnostic(diags, LevelWarn, "", `skill "shared" from builtin shadowed by project`) {
		t.Errorf("missing builtin shadow diagnostic: %#v", diags)
	}
	if !hasDiagnostic(diags, LevelWarn, "/home/me/.agents/skills/shared", `skill "shared" from user shadowed by project`) {
		t.Errorf("missing user shadow diagnostic: %#v", diags)
	}
}

func TestMergeSameSourceKeepsSmallerDir(t *testing.T) {
	t.Parallel()

	catalog, diags := Merge([]Skill{
		{Name: "dup", Description: "b", Source: SourceProject, Dir: "/b/dup"},
		{Name: "dup", Description: "a", Source: SourceProject, Dir: "/a/dup"},
	})
	got, ok := catalog.Lookup("dup")
	if !ok || got.Dir != "/a/dup" {
		t.Fatalf("Lookup(dup) = %#v, want Dir /a/dup", got)
	}
	if !hasDiagnostic(diags, LevelWarn, "/b/dup", `skill "dup" from project shadowed by project`) {
		t.Fatalf("missing same-source shadow diagnostic: %#v", diags)
	}
}

func validSkillMD(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	writeFileBytes(t, path, []byte(content))
}

func writeFileBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func hasDiagnostic(diags []Diagnostic, level Level, dir, substr string) bool {
	for _, diag := range diags {
		if diag.Level == level && diag.Dir == dir && strings.Contains(diag.Message, substr) {
			return true
		}
	}
	return false
}

func TestScanEmptyRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skills, diags, err := Scan(os.DirFS(root), SourceUser, root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(skills) != 0 || len(diags) != 0 {
		t.Fatalf("Scan() skills=%#v diags=%#v, want empty", skills, diags)
	}
}
