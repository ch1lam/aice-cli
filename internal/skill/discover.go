package skill

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
)

// Scan reads the immediate children of fsys for <name>/SKILL.md.
// It does not recurse. A missing root yields an empty result, not an error.
//
// root is the host directory corresponding to fsys when fsys is a real
// filesystem; each loaded skill records Dir as filepath.Join(root, name).
// Pass an empty root for embedded filesystems so Dir stays empty.
func Scan(fsys fs.FS, source Source, root string) ([]Skill, []Diagnostic, error) {
	if root != "" {
		root = filepath.Clean(root)
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Skill{}, []Diagnostic{}, nil
		}
		return nil, nil, fmt.Errorf("skill: read root: %w", err)
	}

	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return cmp.Compare(a.Name(), b.Name())
	})

	skills := make([]Skill, 0)
	diags := make([]Diagnostic, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := skillDir(root, name)
		rel := path.Join(name, "SKILL.md")

		info, err := fs.Stat(fsys, rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: fmt.Sprintf("cannot stat skill.md: %v", err),
			})
			continue
		}
		if !info.Mode().IsRegular() {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: "skill.md is not a regular file",
			})
			continue
		}
		if info.Size() > maxSkillBytes {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: errTooLarge.Error(),
			})
			continue
		}

		data, err := readSkillFile(fsys, rel)
		if err != nil {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: fmt.Sprintf("cannot read skill.md: %v", err),
			})
			continue
		}
		if len(data) > maxSkillBytes {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: errTooLarge.Error(),
			})
			continue
		}

		parsed, err := parseSkillMD(data, name)
		if err != nil {
			diags = append(diags, Diagnostic{
				Level:   LevelError,
				Dir:     dir,
				Message: err.Error(),
			})
			continue
		}
		for _, warning := range parsed.Warnings {
			diags = append(diags, Diagnostic{
				Level:   LevelWarn,
				Dir:     dir,
				Message: warning,
			})
		}
		skills = append(skills, Skill{
			Name:        parsed.Name,
			Description: parsed.Description,
			Source:      source,
			Dir:         dirForSkill(root, name),
			Body:        parsed.Body,
		})
	}

	slices.SortFunc(skills, func(a, b Skill) int {
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Dir, b.Dir)
	})
	slices.SortFunc(diags, compareDiagnostic)
	return skills, diags, nil
}

// Merge combines skills from one or more Scan results. Same-name conflicts
// keep project over user over builtin. Same-source ties keep the skill with
// the lexicographically smaller Dir. Shadowed skills are omitted and reported
// as warnings. The catalog is sorted by name.
func Merge(groups ...[]Skill) (Catalog, []Diagnostic) {
	var all []Skill
	for _, group := range groups {
		all = append(all, group...)
	}
	slices.SortFunc(all, func(a, b Skill) int {
		if n := cmp.Compare(b.rank(), a.rank()); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Dir, b.Dir); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})

	chosen := make(map[string]Skill, len(all))
	diags := make([]Diagnostic, 0)
	for _, item := range all {
		existing, ok := chosen[item.Name]
		if !ok {
			chosen[item.Name] = item
			continue
		}
		diags = append(diags, Diagnostic{
			Level: LevelWarn,
			Dir:   item.Dir,
			Message: fmt.Sprintf(
				"skill %q from %s shadowed by %s",
				item.Name,
				item.Source,
				existing.Source,
			),
		})
	}

	names := make([]string, 0, len(chosen))
	for name := range chosen {
		names = append(names, name)
	}
	slices.Sort(names)
	skills := make([]Skill, 0, len(names))
	for _, name := range names {
		skills = append(skills, chosen[name])
	}
	slices.SortFunc(diags, compareDiagnostic)
	return Catalog{skills: skills, byName: chosen}, diags
}

func (s Skill) rank() int {
	return s.Source.rank()
}

func skillDir(root, name string) string {
	if root == "" {
		return name
	}
	return filepath.Join(root, name)
}

func dirForSkill(root, name string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, name)
}

func readSkillFile(fsys fs.FS, name string) ([]byte, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxSkillBytes+1))
}

func compareDiagnostic(a, b Diagnostic) int {
	if n := cmp.Compare(a.Dir, b.Dir); n != 0 {
		return n
	}
	if n := cmp.Compare(string(a.Level), string(b.Level)); n != 0 {
		return n
	}
	return cmp.Compare(a.Message, b.Message)
}
