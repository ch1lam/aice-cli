package skill

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestBuiltinContainsCreateSkill(t *testing.T) {
	t.Parallel()

	skills, diags, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("Builtin() diags = %#v, want none", diags)
	}
	if len(skills) < 1 {
		t.Fatal("Builtin() returned no skills")
	}

	var create Skill
	found := false
	for _, item := range skills {
		if item.Name == "create-skill" {
			create = item
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Builtin() missing create-skill: %#v", skills)
	}
	if create.Source != SourceBuiltin {
		t.Errorf("Source = %q, want %q", create.Source, SourceBuiltin)
	}
	if create.Dir != "" {
		t.Errorf("Dir = %q, want empty", create.Dir)
	}
	if create.Description == "" {
		t.Fatal("create-skill description is empty")
	}
	if create.Body == "" {
		t.Fatal("create-skill body is empty")
	}
}

func TestBuiltinMatchesMapFSScan(t *testing.T) {
	t.Parallel()

	data, err := fs.ReadFile(builtinFS, "builtin/create-skill/SKILL.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	embedded, embedDiags, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	mapped, mapDiags, err := Scan(fstest.MapFS{
		"create-skill/SKILL.md": {Data: data},
	}, SourceBuiltin, "")
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(embedDiags) != len(mapDiags) {
		t.Fatalf("diags embed=%#v map=%#v", embedDiags, mapDiags)
	}
	if len(embedded) != len(mapped) {
		t.Fatalf("skills embed=%#v map=%#v", embedded, mapped)
	}
	if len(embedded) == 0 {
		t.Fatal("no skills to compare")
	}
	if embedded[0].Name != mapped[0].Name ||
		embedded[0].Description != mapped[0].Description ||
		embedded[0].Source != mapped[0].Source ||
		embedded[0].Dir != mapped[0].Dir ||
		embedded[0].Body != mapped[0].Body {
		t.Fatalf("embed %#v != map %#v", embedded[0], mapped[0])
	}
}
