package tool_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/tool"
)

var _ agent.Tool = (*tool.Skill)(nil)

func TestSkillDefinitionConstrainsNameEnum(t *testing.T) {
	t.Parallel()
	skillTool := tool.NewSkill([]tool.SkillEntry{
		{Name: "zeta", Body: "z"},
		{Name: "alpha", Body: "a"},
		{Name: "", Body: "ignored"},
		{Name: "alpha", Body: "override"},
	})
	definition := skillTool.Definition()
	if definition.Name != "skill" {
		t.Fatalf("Definition().Name = %q, want skill", definition.Name)
	}
	if definition.PromptSnippet == "" {
		t.Fatal("Definition().PromptSnippet is empty")
	}
	if len(definition.PromptGuidelines) == 0 {
		t.Fatal("Definition().PromptGuidelines is empty")
	}
	if !json.Valid(definition.InputSchema) {
		t.Fatalf("Definition().InputSchema is invalid json: %s", definition.InputSchema)
	}

	var schema map[string]any
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	nameSchema, ok := properties["name"].(map[string]any)
	if !ok {
		t.Fatalf("schema name = %#v", properties["name"])
	}
	rawEnum, ok := nameSchema["enum"].([]any)
	if !ok {
		t.Fatalf("schema enum = %#v", nameSchema["enum"])
	}
	got := make([]string, len(rawEnum))
	for i, value := range rawEnum {
		name, ok := value.(string)
		if !ok {
			t.Fatalf("enum[%d] = %#v, want string", i, value)
		}
		got[i] = name
	}
	want := []string{"alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("enum = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("enum = %v, want %v", got, want)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Fatalf("schema required = %#v, want [name]", schema["required"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
}

func TestSkillExecuteReturnsWrappedBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "SKILL.md", "ignored on disk")
	writeFixture(t, dir, "references/x.md", "x")
	writeFixture(t, dir, "scripts/run.sh", "#!/bin/sh")
	writeFixture(t, dir, "nested/SKILL.md", "nested")

	skillTool := tool.NewSkill([]tool.SkillEntry{{
		Name: "pdf-processing",
		Dir:  dir,
		Body: "# PDF Processing\n\nUse this skill when handling PDFs.\n",
	}})
	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{
		"name": "pdf-processing",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() IsError = true, text = %q", resultText(t, result))
	}
	got := resultText(t, result)
	wantPrefix := "<skill_content name=\"pdf-processing\">\n# PDF Processing\n\nUse this skill when handling PDFs.\n\nSkill directory: " + dir + "\nRelative paths in this skill are relative to the skill directory.\n<skill_resources>\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("Execute() text = %q, want prefix %q", got, wantPrefix)
	}
	if strings.Contains(got, "<file>SKILL.md</file>") {
		t.Fatalf("Execute() listed root SKILL.md: %q", got)
	}
	for _, resource := range []string{"nested/SKILL.md", "references/x.md", "scripts/run.sh"} {
		if !strings.Contains(got, "<file>"+resource+"</file>") {
			t.Fatalf("Execute() text = %q, want resource %q", got, resource)
		}
	}
	if !strings.HasSuffix(got, "</skill_resources>\n</skill_content>") {
		t.Fatalf("Execute() text = %q, want closing tags", got)
	}
}

func TestSkillExecuteUnknownNameReturnsToolError(t *testing.T) {
	t.Parallel()
	skillTool := tool.NewSkill([]tool.SkillEntry{{Name: "pdf", Body: "body"}})
	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{
		"name": "missing",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("Execute() IsError = false, want true")
	}
	if got := resultText(t, result); !strings.Contains(got, `"missing"`) {
		t.Fatalf("Execute() text = %q, want unknown skill name", got)
	}
}

func TestSkillExecuteOmitsDirectoryWhenDirEmpty(t *testing.T) {
	t.Parallel()
	skillTool := tool.NewSkill([]tool.SkillEntry{{
		Name: "create-skill",
		Body: "Write a SKILL.md file.\n",
	}})
	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{
		"name": "create-skill",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := resultText(t, result)
	want := "<skill_content name=\"create-skill\">\nWrite a SKILL.md file.\n</skill_content>"
	if got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
	if strings.Contains(got, "Skill directory:") || strings.Contains(got, "skill_resources") {
		t.Fatalf("Execute() included directory listing for empty Dir: %q", got)
	}
}

func TestSkillExecuteTruncatesResourceList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "SKILL.md", "root")
	for i := 0; i < 51; i++ {
		name := filepath.Join("files", string(rune('a'+i/26))+string(rune('a'+i%26))+".md")
		writeFixture(t, dir, name, "x")
	}
	skillTool := tool.NewSkill([]tool.SkillEntry{{
		Name: "wide",
		Dir:  dir,
		Body: "body\n",
	}})
	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{"name": "wide"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := resultText(t, result)
	if strings.Count(got, "<file>") != 50 {
		t.Fatalf("Execute() file tags = %d, want 50\n%s", strings.Count(got, "<file>"), got)
	}
	if !strings.Contains(got, `truncated="true"`) {
		t.Fatalf("Execute() text = %q, want truncated attribute", got)
	}
}

func TestSkillExecuteOmitsResourcesWhenDirUnreadable(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-skill-dir")
	skillTool := tool.NewSkill([]tool.SkillEntry{{
		Name: "broken",
		Dir:  missing,
		Body: "still useful\n",
	}})
	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{"name": "broken"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := resultText(t, result)
	if !strings.Contains(got, "still useful") {
		t.Fatalf("Execute() text = %q, want body", got)
	}
	if !strings.Contains(got, "Skill directory: "+missing) {
		t.Fatalf("Execute() text = %q, want skill directory line", got)
	}
	if strings.Contains(got, "skill_resources") {
		t.Fatalf("Execute() included resources after list failure: %q", got)
	}
}

func TestSkillExecuteEmptyCatalogSchema(t *testing.T) {
	t.Parallel()
	skillTool := tool.NewSkill(nil)
	definition := skillTool.Definition()
	var schema map[string]any
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	properties := schema["properties"].(map[string]any)
	nameSchema := properties["name"].(map[string]any)
	rawEnum, ok := nameSchema["enum"].([]any)
	if !ok {
		t.Fatalf("enum = %#v, want []", nameSchema["enum"])
	}
	if len(rawEnum) != 0 {
		t.Fatalf("enum = %v, want empty", rawEnum)
	}

	result, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{"name": "any"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("Execute() IsError = false, want true")
	}
}

func TestSkillExecuteRejectsUnknownArguments(t *testing.T) {
	t.Parallel()
	skillTool := tool.NewSkill([]tool.SkillEntry{{Name: "pdf", Body: "body"}})
	_, err := skillTool.Execute(t.Context(), toolCall(t, "skill", map[string]any{
		"name": "pdf", "path": "/tmp",
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Execute() error = %v, want unknown-field error", err)
	}
}
