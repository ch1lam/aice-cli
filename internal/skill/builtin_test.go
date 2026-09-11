package skill

import (
	"io/fs"
	"strings"
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

	fixture := fstest.MapFS{}
	err := fs.WalkDir(builtinFS, "builtin", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(builtinFS, path)
		if err != nil {
			return err
		}
		fixture[strings.TrimPrefix(path, "builtin/")] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	embedded, embedDiags, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}
	mapped, mapDiags, err := Scan(fixture, SourceBuiltin, "")
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

func TestBuiltinBrowserContract(t *testing.T) {
	skills, diags, err := Builtin()
	if err != nil || len(diags) != 0 {
		t.Fatalf("%v %v", err, diags)
	}
	for _, item := range skills {
		if item.Name == "browser" {
			if item.Description == "" || item.Dir != "" || item.Source != SourceBuiltin {
				t.Fatalf("invalid browser skill %+v", item)
			}
			for _, want := range []string{"skills get core", ".aice/browser/screenshots/page.png", "tab_gone", "close --all", "AGENT_BROWSER_SESSION"} {
				if !strings.Contains(item.Body, want) {
					t.Errorf("missing %s", want)
				}
			}
			return
		}
	}
	t.Fatal("missing browser skill")
}
